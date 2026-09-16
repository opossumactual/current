package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"current/internal/store"
)

func TestRefreshFeed(t *testing.T) {
	body, _ := os.ReadFile("../feed/testdata/rss.xml")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write(body)
	}))
	defer srv.Close()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, _ := s.CreateFeed(&store.Feed{URL: srv.URL + "/rss"})
	events := make(chan Event, 10)
	c := New(s, srv.Client(), Options{Workers: 2, Events: events})

	ev, err := c.RefreshFeed(context.Background(), f.ID)
	if err != nil || ev.Inserted != 2 {
		t.Fatalf("first refresh: %+v %v", ev, err)
	}
	select {
	case got := <-events:
		if got.FeedID != f.ID || got.Inserted != 2 {
			t.Fatalf("event %+v", got)
		}
	default:
		t.Fatalf("no event emitted")
	}
	g, _ := s.GetFeed(f.ID)
	if g.Title != "Example RSS" || g.SiteURL != "https://example.com/" || g.IconURL != "https://icons.duckduckgo.com/ip3/example.com.ico" || g.ETag != `"v1"` {
		t.Fatalf("metadata: %+v", g)
	}
	if g.LastError != "" || g.LastFetchedAt == nil {
		t.Fatalf("fetch state: %+v", g)
	}
	ev, _ = c.RefreshFeed(context.Background(), f.ID)
	if ev.Inserted != 0 {
		t.Fatalf("second refresh inserted %d", ev.Inserted)
	}
	if hits != 2 {
		t.Fatalf("hits = %d", hits)
	}
	items, _, _ := s.ListArticles(store.ArticleQuery{})
	if len(items) != 2 {
		t.Fatalf("articles = %d", len(items))
	}
	// due scheduling: not due right after a successful fetch
	due, _ := s.FeedsDue(time.Now())
	if len(due) != 0 {
		t.Fatalf("should not be due")
	}
}

func TestRefreshRecordsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	s, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	f, _ := s.CreateFeed(&store.Feed{URL: srv.URL})
	c := New(s, srv.Client(), Options{})
	c.RefreshAll(context.Background())
	g, _ := s.GetFeed(f.ID)
	if g.LastError == "" {
		t.Fatalf("error not recorded")
	}
}

// The scheduler must fetch newly queued subscriptions and leave paused/archived ones alone.
func TestSchedulerRunsDueFeeds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	body, err := os.ReadFile("../feed/testdata/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	active, _ := st.CreateFeed(&store.Feed{URL: srv.URL + "/active"})
	paused, _ := st.CreateFeed(&store.Feed{URL: srv.URL + "/paused"})
	archived, _ := st.CreateFeed(&store.Feed{URL: srv.URL + "/archived"})
	yes := true
	st.UpdateFeed(paused.ID, store.FeedUpdate{Paused: &yes})
	st.UnsubscribeFeed(archived.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	cr := New(st, srv.Client(), Options{Tick: 10 * time.Millisecond, HostGap: time.Millisecond})
	go func() { defer close(done); cr.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f, _ := st.GetFeed(active.ID)
		if f.Unread == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	a, _ := st.GetFeed(active.ID)
	p, _ := st.GetFeed(paused.ID)
	u, _ := st.GetFeed(archived.ID)
	if a.Unread != 2 || p.LastFetchedAt != nil || u.LastFetchedAt != nil {
		t.Fatalf("active=%+v paused=%+v archived=%+v", a, p, u)
	}
}
