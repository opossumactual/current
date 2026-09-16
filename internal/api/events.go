package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"current/internal/crawler"
)

// Broker fans crawler events out to SSE subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan crawler.Event]struct{}
}

// NewBroker creates a Broker.
func NewBroker() *Broker { return &Broker{subs: map[chan crawler.Event]struct{}{}} }

// Run forwards events from in to all subscribers until in is closed.
func (b *Broker) Run(in <-chan crawler.Event) {
	for ev := range in {
		b.publish(ev)
	}
}

func (b *Broker) RunContext(ctx context.Context, in <-chan crawler.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			b.publish(ev)
		}
	}
}

func (b *Broker) publish(ev crawler.Event) {
	b.mu.Lock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *Broker) subscribe() chan crawler.Event {
	ch := make(chan crawler.Event, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) unsubscribe(ch chan crawler.Event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ch := s.broker.subscribe()
	defer s.broker.unsubscribe(ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: refresh\ndata: %s\n\n", data)
			fl.Flush()
		}
	}
}
