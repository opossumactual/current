package crawler

import (
	"context"
	"current/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestManualRefreshProgress(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			http.Error(w, "failed", 500)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.Write([]byte(`<rss version="2.0"><channel><title>Test</title><item><guid>one</guid><title>New story</title></item></channel></rss>`))
	}))
	defer srv.Close()
	for _, path := range []string{"/good", "/bad", "/paused", "/removed"} {
		f, err := s.CreateFeed(&store.Feed{URL: srv.URL + path})
		if err != nil {
			t.Fatal(err)
		}
		if path == "/paused" {
			yes := true
			s.UpdateFeed(f.ID, store.FeedUpdate{Paused: &yes})
		}
		if path == "/removed" {
			yes := true
			s.UpdateFeed(f.ID, store.FeedUpdate{Unsubscribed: &yes})
		}
	}
	c := New(s, srv.Client(), Options{Workers: 2, Tick: time.Hour, HostGap: time.Millisecond})
	p, err := c.StartRefresh()
	if err != nil || p.Total != 2 || !p.Running {
		t.Fatalf("start: %+v %v", p, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("manual refresh must wake the crawler immediately")
	}
	joined, err := c.StartRefresh()
	if err != nil || joined.ID != p.ID {
		close(release)
		t.Fatalf("duplicate batch: %+v %v", joined, err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for c.Progress().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	p = c.Progress()
	if p.Running || p.Completed != 2 || p.Failed != 1 || p.Inserted != 1 || p.FinishedAt == nil {
		t.Fatalf("finished: %+v", p)
	}
	// A late duplicate completion must not double-count a feed.
	c.complete(Event{FeedID: 1, Inserted: 100})
	if c.Progress().Inserted != 1 {
		t.Fatal("duplicate completion counted")
	}
}

func TestEmptyAndPausedRefreshCompletes(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := New(s, nil, Options{Tick: time.Hour})
	p, err := c.StartRefresh()
	if err != nil || p.Running || p.Total != 0 || p.FinishedAt == nil {
		t.Fatalf("empty: %+v %v", p, err)
	}
	f, _ := s.CreateFeed(&store.Feed{URL: "https://example.com/rss"})
	c.StartRefresh()
	yes := true
	s.UpdateFeed(f.ID, store.FeedUpdate{Paused: &yes})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(time.Second)
	for c.Progress().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p := c.Progress(); p.Running || p.Completed != 1 {
		t.Fatalf("paused after queuing: %+v", p)
	}
}
