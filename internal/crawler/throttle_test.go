package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"current/internal/store"
)

func TestHostKey(t *testing.T) {
	cases := map[string]string{
		"https://www.reddit.com/r/homelab/.rss": "reddit.com",
		"https://old.reddit.com/r/sdr/.rss":     "reddit.com",
		"https://Example.com/feed":              "example.com",
		"not a url":                             "not a url",
	}
	for in, want := range cases {
		if got := hostKey(in); got != want {
			t.Errorf("hostKey(%q) = %q, want %q", in, got, want)
		}
	}
	th := newHostThrottle(time.Second)
	if th.gapFor("reddit.com") != 12*time.Second || th.gapFor("old.reddit.com") != 12*time.Second || th.gapFor("example.com") != time.Second {
		t.Fatalf("gapFor wrong")
	}
}

// Three feeds on one host with four workers must be fetched one at a time,
// spaced by at least the gap; a feed on another host is not delayed by them.
func TestRefreshThrottlesPerHost(t *testing.T) {
	body, _ := os.ReadFile("../feed/testdata/rss.xml")
	var inflight, maxInflight int32
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			m := atomic.LoadInt32(&maxInflight)
			if n <= m || atomic.CompareAndSwapInt32(&maxInflight, m, n) {
				break
			}
		}
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Write(body)
	}))
	defer srv.Close()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{"/a", "/b", "/c"} {
		if _, err := s.CreateFeed(&store.Feed{URL: srv.URL + p}); err != nil {
			t.Fatal(err)
		}
	}
	c := New(s, srv.Client(), Options{Workers: 4, HostGap: 100 * time.Millisecond})
	start := time.Now()
	c.RefreshAll(context.Background())
	if atomic.LoadInt32(&maxInflight) != 1 {
		t.Fatalf("max concurrent requests to one host = %d, want 1", maxInflight)
	}
	if len(times) != 3 {
		t.Fatalf("requests = %d", len(times))
	}
	for i := 1; i < len(times); i++ {
		if d := times[i].Sub(times[i-1]); d < 100*time.Millisecond {
			t.Fatalf("request %d only %v after previous", i, d)
		}
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("took too long: %v", el)
	}
	feeds, _ := s.ListFeeds()
	for _, f := range feeds {
		if f.LastError != "" {
			t.Fatalf("feed %s error %q", f.URL, f.LastError)
		}
	}
}

func TestAcquireHonoursContext(t *testing.T) {
	th := newHostThrottle(time.Hour)
	rel, ok := th.acquire(context.Background(), "https://example.com/1")
	if !ok {
		t.Fatal("first acquire failed")
	}
	rel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, ok := th.acquire(ctx, "https://example.com/2"); ok {
		t.Fatal("second acquire should fail once ctx expires")
	}
}

// A 429 with Retry-After must delay the next request to that host.
func TestHoldAfterRateLimit(t *testing.T) {
	body, _ := os.ReadFile("../feed/testdata/rss.xml")
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{"/a", "/b"} {
		if _, err := s.CreateFeed(&store.Feed{URL: srv.URL + p}); err != nil {
			t.Fatal(err)
		}
	}
	c := New(s, srv.Client(), Options{Workers: 2, HostGap: 10 * time.Millisecond})
	c.RefreshAll(context.Background())
	if len(times) != 2 {
		t.Fatalf("requests = %d", len(times))
	}
	if d := times[1].Sub(times[0]); d < time.Second {
		t.Fatalf("second request only %v after the 429, want >= 1s", d)
	}
}

// A host held beyond MaxHostWait is deferred immediately instead of blocking.
func TestAcquireDefersLongHold(t *testing.T) {
	th := newHostThrottle(time.Millisecond)
	rel, ok := th.acquire(context.Background(), "https://held.test/a")
	if !ok {
		t.Fatal("first acquire failed")
	}
	th.hold("https://held.test/a", MaxHostWait+time.Minute)
	rel()
	start := time.Now()
	if _, ok := th.acquire(context.Background(), "https://held.test/b"); ok {
		t.Fatal("should defer while held")
	}
	if time.Since(start) > time.Second {
		t.Fatal("deferral should not block")
	}
	if _, ok := th.acquire(context.Background(), "https://other.test/"); !ok {
		t.Fatal("other host must not be affected")
	}
}
