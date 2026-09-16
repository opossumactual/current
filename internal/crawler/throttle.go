package crawler

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultHostGap is the minimum spacing between two requests to the same host.
const DefaultHostGap = 2 * time.Second

// MaxHostWait is the longest acquire will block a worker for one host; a
// longer wait defers the feed to a later tick instead of tying up the pool.
const MaxHostWait = 15 * time.Second

// hostGaps overrides the gap for hosts known to rate-limit aggressively.
var hostGaps = map[string]time.Duration{
	"reddit.com": 12 * time.Second,
}

type hostState struct {
	mu        sync.Mutex
	last      time.Time
	notBefore time.Time // server-requested hold, see hold()
}

// hostThrottle serialises requests per host and enforces a minimum gap.
type hostThrottle struct {
	mu    sync.Mutex
	hosts map[string]*hostState
	gap   time.Duration
	sleep func(context.Context, time.Duration) bool
}

func newHostThrottle(gap time.Duration) *hostThrottle {
	if gap <= 0 {
		gap = DefaultHostGap
	}
	return &hostThrottle{hosts: map[string]*hostState{}, gap: gap, sleep: sleepCtx}
}

// hostKey normalises a URL to a throttling key: the registrable-ish host without "www.".
func hostKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return rawURL
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	h = strings.TrimPrefix(h, "old.")
	return h
}

func (t *hostThrottle) gapFor(host string) time.Duration {
	for k, g := range hostGaps {
		if host == k || strings.HasSuffix(host, "."+k) {
			return g
		}
	}
	return t.gap
}

// acquire blocks until the host is free and the gap has elapsed. The returned
// release must be called when the request is done. It returns false when ctx
// ends or when the host is held for longer than MaxHostWait (the caller
// should then leave the feed for a later tick).
func (t *hostThrottle) acquire(ctx context.Context, rawURL string) (release func(), ok bool) {
	host := hostKey(rawURL)
	t.mu.Lock()
	hs := t.hosts[host]
	if hs == nil {
		hs = &hostState{}
		t.hosts[host] = hs
	}
	t.mu.Unlock()

	hs.mu.Lock()
	wait := t.gapFor(host) - time.Since(hs.last)
	if hold := time.Until(hs.notBefore); hold > wait {
		wait = hold
	}
	if wait > MaxHostWait {
		hs.mu.Unlock()
		return func() {}, false
	}
	if wait > 0 {
		if !t.sleep(ctx, wait) {
			hs.mu.Unlock()
			return func() {}, false
		}
	}
	return func() {
		hs.last = time.Now()
		hs.mu.Unlock()
	}, true
}

// hold delays the next request to rawURL's host by d, as asked by the server
// (Retry-After, exhausted X-RateLimit budget).
func (t *hostThrottle) hold(rawURL string, d time.Duration) {
	if d <= 0 {
		return
	}
	host := hostKey(rawURL)
	t.mu.Lock()
	hs := t.hosts[host]
	if hs == nil {
		hs = &hostState{}
		t.hosts[host] = hs
	}
	t.mu.Unlock()
	// hold is called by the goroutine that still owns hs.mu via acquire, so
	// notBefore is written without taking the lock again.
	if nb := time.Now().Add(d); nb.After(hs.notBefore) {
		hs.notBefore = nb
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}
