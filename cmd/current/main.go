package main

import (
	"context"
	"crypto/sha256"
	"current/internal/crawler"
	"current/internal/feed"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"current/internal/api"
	"current/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8490", "listen address")
	data := flag.String("data", "data/library.db", "SQLite library path")
	tick := flag.Duration("poll-interval", 15*time.Second, "check for due subscriptions")
	flag.Parse()
	if ua := os.Getenv("CURRENT_USER_AGENT"); ua != "" {
		feed.UserAgent = ua
	}
	host, _, err := net.SplitHostPort(*addr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		log.Fatal("Current serves a personal library: --addr must use a loopback IP")
	}
	if *tick < time.Second {
		log.Fatal("poll-interval must be at least 1s")
	}
	if err := os.MkdirAll(filepath.Dir(*data), 0700); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	events := make(chan crawler.Event, 64)
	cr := crawler.New(st, nil, crawler.Options{Workers: 4, Tick: *tick, Events: events, Logger: log.Default()})
	broker := api.NewBroker()
	crawlerDone := make(chan struct{})
	go broker.RunContext(ctx, events)
	go func() { defer close(crawlerDone); ; cr.Run(ctx) }()
	base := api.New(st, cr, broker, "", log.Default()).Handler()
	cacheDir := filepath.Join(filepath.Dir(*data), "images")
	os.MkdirAll(cacheDir, 0700)
	slots := make(chan struct{}, 4)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				return nil, fmt.Errorf("non-public image address")
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no image address")
		}
		return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost, _, hostErr := net.SplitHostPort(r.Host)
		if hostErr != nil {
			requestHost = r.Host
		}
		requestIP := net.ParseIP(requestHost)
		if requestHost != "localhost" && (requestIP == nil || !requestIP.IsLoopback()) {
			http.Error(w, "host rejected", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' https: http: data:; font-src 'self'; connect-src 'self'; frame-src https://www.youtube.com https://www.youtube-nocookie.com; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/image/") {
			id, e := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/image/"), 10, 64)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			a, e := st.GetArticle(id)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			images := feed.ArticleImages(a)
			index := 0
			if raw := r.URL.Query().Get("index"); raw != "" {
				index, e = strconv.Atoi(raw)
			}
			if e != nil || index < 0 || index >= len(images) {
				http.NotFound(w, r)
				return
			}
			imageURL := images[index].URL
			// A changed full-text image list must not reuse a cached index's old image.
			file := filepath.Join(cacheDir, fmt.Sprintf("%d-%x", id, sha256.Sum256([]byte(imageURL))))
			if b, e := os.ReadFile(file); e == nil {
				w.Header().Set("Content-Type", http.DetectContentType(b))
				w.Header().Set("Cache-Control", "private, max-age=86400")
				w.Write(b)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-r.Context().Done():
				return
			}
			if b, e := os.ReadFile(file); e == nil {
				w.Header().Set("Content-Type", http.DetectContentType(b))
				w.Write(b)
				return
			}
			req, e := http.NewRequestWithContext(r.Context(), "GET", imageURL, nil)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 CurrentReader/0.1")
			resp, e := client.Do(req)
			if e != nil {
				http.Error(w, "Image unavailable", 502)
				return
			}
			defer resp.Body.Close()
			b, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
			ct := http.DetectContentType(b)
			if e != nil || resp.StatusCode != 200 || len(b) > 8<<20 || !strings.HasPrefix(ct, "image/") {
				http.Error(w, "Image unavailable", 502)
				return
			}
			os.WriteFile(file, b, 0600)
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Write(b)
			return
		}
		base.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("Current: http://%s · library %s · live feed polling enabled", *addr, *data)
	serveErr := srv.ListenAndServe()
	stop()
	<-crawlerDone
	if serveErr != nil && serveErr != http.ErrServerClosed {
		st.Close()
		log.Fatal(serveErr)
	}
}
