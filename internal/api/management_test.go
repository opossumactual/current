package api

import (
	"current/internal/store"
	"fmt"
	"testing"
	"time"
)

func TestManageSubscriptionsAndPreserveHistory(t *testing.T) {
	e := newEnv(t, "")
	var f store.Feed
	if code := e.do("POST", "/api/feeds", map[string]any{"url": e.feed.URL + "/rss", "direct": true}, &f); code != 201 {
		t.Fatalf("create %d", code)
	}
	route := fmt.Sprintf("/api/feeds/%d", f.ID)
	for _, change := range []map[string]any{{"paused": "true"}, {"categoryId": 1.5}, {"fetchIntervalMin": 10.3}, {"url": "file:///tmp/private"}, {"title": 123}} {
		if code := e.do("PATCH", route, change, nil); code != 400 {
			t.Fatalf("validation %v: %d", change, code)
		}
	}
	if code := e.do("PATCH", route, map[string]any{"paused": true}, &f); code != 200 || !f.Paused {
		t.Fatalf("pause %d %+v", code, f)
	}
	e.st.ScheduleAll()
	due, err := e.st.FeedsDue(time.Now().Add(48 * time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("paused feed was due: %+v %v", due, err)
	}
	if f.LastSuccessAt == nil {
		t.Fatal("successful fetch was not recorded")
	}
	if code := e.do("DELETE", route, nil, nil); code != 204 {
		t.Fatal(code)
	}
	var feeds []store.Feed
	e.do("GET", "/api/feeds", nil, &feeds)
	if len(feeds) != 0 {
		t.Fatal("unsubscribed feed still active")
	}
	e.do("GET", "/api/feeds?includeArchived=1", nil, &feeds)
	if len(feeds) != 1 || !feeds[0].Unsubscribed {
		t.Fatal("missing archived feed")
	}
	var lib store.Library
	e.do("GET", "/api/library", nil, &lib)
	if lib.Feeds != 0 || lib.Archived != 1 || lib.Articles != 2 {
		t.Fatalf("library %+v", lib)
	}
	var restored store.Feed
	e.do("POST", "/api/feeds", map[string]any{"url": e.feed.URL + "/rss", "direct": true}, &restored)
	if restored.ID != f.ID || restored.Unsubscribed || restored.Paused || restored.Unread != 2 {
		t.Fatalf("restore must keep ID and articles: %+v", restored)
	}
	if code := e.do("PATCH", route, map[string]any{"categoryId": nil, "fetchIntervalMin": 5}, &restored); code != 200 {
		t.Fatal(code)
	}
	due, _ = e.st.FeedsDue(time.Now())
	if len(due) != 1 {
		t.Fatal("interval change should reschedule feed")
	}
}
