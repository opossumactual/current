package store

import (
	"errors"
	"testing"
	"time"
)

func TestCategoriesCRUD(t *testing.T) {
	s := openTest(t)
	c, err := s.CreateCategory("Blogs")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.Title != "Blogs" {
		t.Fatalf("bad category %+v", c)
	}
	c2, _ := s.CreateCategory("Comics")
	if c2.Position <= c.Position {
		t.Fatalf("position not increasing: %d <= %d", c2.Position, c.Position)
	}
	same, err := s.FindOrCreateCategory("Blogs")
	if err != nil || same.ID != c.ID {
		t.Fatalf("FindOrCreate returned %+v %v", same, err)
	}
	if err := s.UpdateCategory(c.ID, "Weblogs", 5); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListCategories()
	if len(list) != 2 || list[0].Title != "Comics" || list[1].Title != "Weblogs" {
		t.Fatalf("unexpected list %+v", list)
	}
	f, err := s.CreateFeed(&Feed{URL: "https://a.example/rss", Title: "A", CategoryID: &c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCategory(c.ID); err != nil {
		t.Fatal(err)
	}
	f, _ = s.GetFeed(f.ID)
	if f.CategoryID != nil {
		t.Fatalf("feed should be uncategorized after category delete")
	}
	if err := s.DeleteCategory(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFeedsCRUD(t *testing.T) {
	s := openTest(t)
	f, err := s.CreateFeed(&Feed{URL: "https://a.example/rss", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if f.FetchIntervalMin != 60 {
		t.Fatalf("default interval = %d", f.FetchIntervalMin)
	}
	if _, err := s.CreateFeed(&Feed{URL: "https://a.example/rss"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	title := "Renamed"
	ex := true
	if err := s.UpdateFeed(f.ID, FeedUpdate{Title: &title, ExtractFulltext: &ex}); err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetFeed(f.ID)
	if g.Title != "Renamed" || !g.ExtractFulltext {
		t.Fatalf("update not applied: %+v", g)
	}
	c, _ := s.CreateCategory("X")
	cid := &c.ID
	if err := s.UpdateFeed(f.ID, FeedUpdate{CategoryID: &cid}); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GetFeed(f.ID)
	if g.CategoryID == nil || *g.CategoryID != c.ID {
		t.Fatalf("category not set")
	}
	var none *int64
	if err := s.UpdateFeed(f.ID, FeedUpdate{CategoryID: &none}); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GetFeed(f.ID)
	if g.CategoryID != nil {
		t.Fatalf("category not cleared")
	}
	byURL, err := s.GetFeedByURL("https://a.example/rss")
	if err != nil || byURL.ID != f.ID {
		t.Fatalf("GetFeedByURL: %v", err)
	}
	list, _ := s.ListFeeds()
	if len(list) != 1 {
		t.Fatalf("list len %d", len(list))
	}
	if err := s.DeleteFeed(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFeed(f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFeedsDueAndBackoff(t *testing.T) {
	s := openTest(t)
	f, _ := s.CreateFeed(&Feed{URL: "https://a.example/rss"})
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	due, _ := s.FeedsDue(now)
	if len(due) != 1 {
		t.Fatalf("new feed should be due, got %d", len(due))
	}
	if err := s.MarkFetched(f.ID, `"e1"`, "Mon", nil, now); err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetFeed(f.ID)
	if g.ETag != `"e1"` || g.LastModified != "Mon" || g.LastFetchedAt == nil || !g.LastFetchedAt.Equal(now) {
		t.Fatalf("fetch metadata not stored: %+v", g)
	}
	if !g.NextFetchAt.Equal(now.Add(60 * time.Minute)) {
		t.Fatalf("next fetch = %v", g.NextFetchAt)
	}
	due, _ = s.FeedsDue(now.Add(10 * time.Minute))
	if len(due) != 0 {
		t.Fatalf("should not be due yet")
	}
	due, _ = s.FeedsDue(now.Add(61 * time.Minute))
	if len(due) != 1 {
		t.Fatalf("should be due after interval")
	}
	// failures back off from the 60 min base: 120, 240, 360 (cap)
	want := []time.Duration{120, 240, 360, 360, 360}
	for i, w := range want {
		if err := s.MarkFetched(f.ID, "", "", errors.New("boom"), now); err != nil {
			t.Fatal(err)
		}
		g, _ = s.GetFeed(f.ID)
		if g.LastError != "boom" {
			t.Fatalf("error not recorded")
		}
		if got := g.NextFetchAt.Sub(now); got != w*time.Minute {
			t.Fatalf("failure %d: next in %v, want %v", i+1, got, w*time.Minute)
		}
	}
	if err := s.MarkFetched(f.ID, "", "", nil, now); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GetFeed(f.ID)
	if g.LastError != "" || g.NextFetchAt.Sub(now) != 60*time.Minute || g.FetchIntervalMin != 60 {
		t.Fatalf("success should reset backoff: %+v", g)
	}
	if err := s.ScheduleNow(f.ID); err != nil {
		t.Fatal(err)
	}
	due, _ = s.FeedsDue(now)
	if len(due) != 1 {
		t.Fatalf("ScheduleNow should make it due")
	}
}
