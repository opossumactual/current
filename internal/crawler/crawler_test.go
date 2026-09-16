package crawler

import (
	"context"
	"errors"
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

// Signal when a caller starts waiting on its context, so the fixture can keep
// the scheduled request blocked until the manual request has joined it.
type refreshWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *refreshWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestRefreshFeedJoinsScheduledFetch(t *testing.T) {
	for _, name := range []string{"success", "failure", "cancel waiter", "edited URL"} {
		t.Run(name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 1 {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if name == "failure" || (name == "edited URL" && r.URL.Path == "/original") {
					http.Error(w, "unavailable", http.StatusInternalServerError)
					return
				}
				w.Write([]byte(`<rss version="2.0"><channel><title>Test</title><item><guid>one</guid><title>New story</title></item></channel></rss>`))
			}))
			defer srv.Close()
			defer unblock()
			f, err := st.CreateFeed(&store.Feed{URL: srv.URL + "/original"})
			if err != nil {
				t.Fatal(err)
			}
			cr := New(st, srv.Client(), Options{HostGap: time.Millisecond})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			scheduledDone := make(chan struct{})
			go func() {
				defer close(scheduledDone)
				cr.refreshMany(ctx, []store.Feed{*f})
			}()
			defer func() { unblock(); cancel(); <-scheduledDone }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("scheduled fetch did not start")
			}
			if name == "edited URL" {
				url := srv.URL + "/repaired"
				if err := st.UpdateFeed(f.ID, store.FeedUpdate{URL: &url}); err != nil {
					t.Fatal(err)
				}
			}
			manualCtx, cancelManual := context.WithCancel(ctx)
			waitCtx := &refreshWaitContext{Context: manualCtx, waiting: make(chan struct{})}
			type result struct {
				event Event
				err   error
			}
			results := make(chan result, 1)
			manualDone := make(chan struct{})
			go func() {
				defer close(manualDone)
				ev, err := cr.RefreshFeed(waitCtx, f.ID)
				results <- result{ev, err}
			}()
			defer func() { unblock(); cancelManual(); <-manualDone }()
			select {
			case <-waitCtx.waiting:
			case got := <-results:
				t.Fatalf("manual refresh returned before the scheduled fetch finished: %+v", got)
			case <-ctx.Done():
				t.Fatal("manual refresh did not join the scheduled fetch")
			}
			if name == "cancel waiter" {
				cancelManual()
				got := <-results
				if !errors.Is(got.err, context.Canceled) {
					t.Fatalf("cancelled waiter: %+v", got)
				}
				unblock()
			} else {
				unblock()
				got := <-results
				if got.err != nil || got.event.FeedID != f.ID {
					t.Fatalf("manual refresh: %+v", got)
				}
				if name == "failure" {
					if got.event.Error != "HTTP 500" {
						t.Fatalf("lost fetch error: %+v", got)
					}
				} else if got.event.Error != "" || got.event.Inserted != 1 {
					t.Fatalf("lost fetch result: %+v", got)
				}
			}
			<-scheduledDone
			wantHits := int32(1)
			if name == "edited URL" {
				wantHits = 2
			}
			if hits.Load() != wantHits {
				t.Fatalf("fetches = %d, want %d", hits.Load(), wantHits)
			}
			stored, err := st.GetFeed(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "failure" {
				if stored.LastError != "HTTP 500" {
					t.Fatalf("fetch error not persisted before returning: %+v", stored)
				}
			} else if stored.LastError != "" || stored.Unread != 1 {
				t.Fatalf("fetch did not finish successfully: %+v", stored)
			}
		})
	}
}

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
