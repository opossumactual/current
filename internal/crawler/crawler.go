// Package crawler refreshes feeds on a schedule.
package crawler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"current/internal/feed"
	"current/internal/store"
)

// Event reports a completed refresh.
type Event struct {
	FeedID   int64  `json:"feedId"`
	Inserted int    `json:"inserted"`
	Error    string `json:"error,omitempty"`
}

// Options configures a Crawler.
type Options struct {
	Workers int
	Tick    time.Duration
	Events  chan<- Event
	Logger  *log.Logger
	// HostGap is the minimum spacing between requests to one host (default DefaultHostGap).
	HostGap time.Duration
}

// Crawler polls due feeds with a worker pool.
type Crawler struct {
	store    *store.Store
	client   *http.Client
	opts     Options
	mu       sync.Mutex
	active   map[int64]bool
	wake     chan struct{}
	progress Progress
	pending  map[int64]bool
	throttle *hostThrottle
}

// New creates a Crawler.
func New(s *store.Store, client *http.Client, opts Options) *Crawler {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Tick <= 0 {
		opts.Tick = time.Minute
	}
	if client == nil {
		client = feed.NewClient()
	}
	return &Crawler{store: s, client: client, opts: opts, active: map[int64]bool{}, wake: make(chan struct{}, 1), throttle: newHostThrottle(opts.HostGap)}
}

// Run polls until ctx is done.
func (c *Crawler) Run(ctx context.Context) {
	t := time.NewTicker(c.opts.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.wake:
		}
		due, err := c.store.FeedsDue(time.Now())
		if err != nil {
			c.logf("feeds due: %v", err)
			continue
		}
		c.refreshMany(ctx, due)
		// A feed can be paused/deleted after the batch was queued. Resolve
		// those entries instead of leaving the progress indicator stuck.
		c.mu.Lock()
		ids := make([]int64, 0, len(c.pending))
		for id := range c.pending {
			ids = append(ids, id)
		}
		c.mu.Unlock()
		for _, id := range ids {
			f, err := c.store.GetFeed(id)
			if err != nil || f.Paused || f.Unsubscribed {
				c.complete(Event{FeedID: id})
			}
		}
	}
}

// RefreshAll refreshes every feed regardless of schedule.
func (c *Crawler) RefreshAll(ctx context.Context) {
	feeds, err := c.store.ListFeeds()
	if err != nil {
		c.logf("list feeds: %v", err)
		return
	}
	c.refreshMany(ctx, feeds)
}

func (c *Crawler) refreshMany(ctx context.Context, feeds []store.Feed) {
	jobs := make(chan store.Feed)
	var wg sync.WaitGroup
	for i := 0; i < c.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				latest, err := c.store.GetFeed(f.ID)
				if err == nil && !latest.Paused && !latest.Unsubscribed {
					c.refresh(ctx, *latest, false)
				}
			}
		}()
	}
	for _, f := range feeds {
		if f.Paused || f.Unsubscribed {
			continue
		}
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- f:
		}
	}
	close(jobs)
	wg.Wait()
}

// RefreshFeed refreshes one feed now.
func (c *Crawler) RefreshFeed(ctx context.Context, id int64) (Event, error) {
	f, err := c.store.GetFeed(id)
	if err != nil {
		return Event{}, err
	}
	if f.Unsubscribed {
		return Event{}, fmt.Errorf("feed is unsubscribed")
	}
	return c.refresh(ctx, *f, true), nil
}

func (c *Crawler) refresh(ctx context.Context, f store.Feed, allowPaused bool) (ev Event) {
	// A feed can be unsubscribed while it is queued behind another host request.
	current, err := c.store.GetFeed(f.ID)
	if err != nil || current.Unsubscribed || (current.Paused && !allowPaused) {
		return Event{FeedID: f.ID}
	}
	f = *current
	c.mu.Lock()
	if c.active[f.ID] {
		c.mu.Unlock()
		return Event{FeedID: f.ID}
	}
	c.active[f.ID] = true
	c.mu.Unlock()
	defer func() {
		c.complete(ev)
		c.mu.Lock()
		delete(c.active, f.ID)
		c.mu.Unlock()
	}()

	ev = Event{FeedID: f.ID}
	// Wait for the host slot with the outer context so a long hold does not
	// eat into the per-fetch timeout; a deferred feed stays due for next tick.
	release, ok := c.throttle.acquire(ctx, f.URL)
	if !ok {
		ev.Error = "Refresh cancelled"
		return ev
	}
	current, err = c.store.GetFeed(f.ID)
	if err != nil || current.Unsubscribed || (current.Paused && !allowPaused) {
		release()
		return ev
	}
	f = *current
	fctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	now := time.Now().UTC()
	res, err := feed.Fetch(fctx, c.client, f.URL, f.ETag, f.LastModified)
	if res != nil && res.RetryAfter > 0 {
		c.throttle.hold(f.URL, res.RetryAfter)
	}
	release()
	if err != nil {
		c.logf("fetch %s: %v", f.URL, err)
		ev.Error = err.Error()
		c.store.MarkFetched(f.ID, f.ETag, f.LastModified, err, now)
		return ev
	}
	if res.NotModified {
		c.store.MarkFetched(f.ID, res.ETag, res.LastModified, nil, now)
		c.emit(ev)
		return ev
	}
	pf, err := feed.Parse(res.Body, f.URL)
	if err != nil {
		c.logf("parse %s: %v", f.URL, err)
		ev.Error = err.Error()
		c.store.MarkFetched(f.ID, f.ETag, f.LastModified, err, now)
		return ev
	}
	current, err = c.store.GetFeed(f.ID)
	if err != nil || current.Unsubscribed || (current.Paused && !allowPaused) {
		return ev
	}
	if f.ExtractFulltext {
		for i := range pf.Items {
			it := &pf.Items[i]
			if len(it.Content) >= feed.MinFulltextLen || it.URL == "" {
				continue
			}
			if c.articleExists(f.ID, it.GUID) {
				continue
			}
			if full, err := feed.Extract(fctx, c.client, it.URL); err == nil {
				it.Content = full
				it.Summary = feed.Summarize(full, feed.SummaryLen)
			}
		}
	}
	n, err := c.store.UpsertArticles(f.ID, pf.Items)
	if err != nil {
		c.logf("upsert %s: %v", f.URL, err)
		ev.Error = err.Error()
		c.store.MarkFetched(f.ID, f.ETag, f.LastModified, err, now)
		return ev
	}
	ev.Inserted = n
	c.fillMetadata(f, pf)
	c.store.MarkFetched(f.ID, res.ETag, res.LastModified, nil, now)
	c.emit(ev)
	return ev
}

func (c *Crawler) articleExists(feedID int64, guid string) bool {
	return c.store.ArticleExists(feedID, guid)
}

func (c *Crawler) fillMetadata(f store.Feed, pf *feed.ParsedFeed) {
	u := store.FeedUpdate{}
	if f.Title == "" && pf.Title != "" {
		u.Title = &pf.Title
	}
	if f.SiteURL == "" && pf.SiteURL != "" {
		u.SiteURL = &pf.SiteURL
	}
	if f.Description == "" && pf.Description != "" {
		u.Description = &pf.Description
	}
	if f.IconURL == "" {
		site := f.SiteURL
		if site == "" {
			site = pf.SiteURL
		}
		if icon := IconURL(site, f.URL); icon != "" {
			u.IconURL = &icon
		}
	}
	if u.Title != nil || u.SiteURL != nil || u.Description != nil || u.IconURL != nil {
		c.store.UpdateFeed(f.ID, u)
	}
}

// IconURL derives a favicon URL from the site URL, falling back to the feed URL host.
func IconURL(siteURL, feedURL string) string {
	for _, s := range []string{siteURL, feedURL} {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return "https://icons.duckduckgo.com/ip3/" + u.Hostname() + ".ico"
		}
	}
	return ""
}

func (c *Crawler) emit(ev Event) {
	if c.opts.Events == nil {
		return
	}
	select {
	case c.opts.Events <- ev:
	default:
	}
}

func (c *Crawler) logf(format string, args ...any) {
	if c.opts.Logger != nil {
		c.opts.Logger.Printf(format, args...)
	}
}
