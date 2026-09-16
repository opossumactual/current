package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func seedArticles(t *testing.T, s *Store, feedID int64, n int, base time.Time) {
	t.Helper()
	var items []Article
	for i := 0; i < n; i++ {
		items = append(items, Article{
			GUID: fmt.Sprintf("g%d", i), Title: fmt.Sprintf("Title %d", i), URL: fmt.Sprintf("https://x/%d", i),
			Content: fmt.Sprintf("<p>body %d gopher</p>", i), Summary: "s", PublishedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	if _, err := s.UpsertArticles(feedID, items); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertDedupesAndKeepsState(t *testing.T) {
	s := openTest(t)
	f, _ := s.CreateFeed(&Feed{URL: "https://a/rss", Title: "A"})
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	n, err := s.UpsertArticles(f.ID, []Article{{GUID: "1", Title: "one", PublishedAt: base}, {GUID: "2", Title: "two", PublishedAt: base}})
	if err != nil || n != 2 {
		t.Fatalf("inserted %d, err %v", n, err)
	}
	list, _, _ := s.ListArticles(ArticleQuery{})
	if err := s.SetRead([]int64{list[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStarred(list[0].ID, true); err != nil {
		t.Fatal(err)
	}
	n, err = s.UpsertArticles(f.ID, []Article{{GUID: "1", Title: "one changed", PublishedAt: base}, {GUID: "3", Title: "three", PublishedAt: base}})
	if err != nil || n != 1 {
		t.Fatalf("second upsert inserted %d, err %v", n, err)
	}
	a, _ := s.GetArticle(list[0].ID)
	if !a.Read || !a.Starred || a.Title != a.Title {
		t.Fatalf("state lost: %+v", a)
	}
	if a.FeedTitle != "A" {
		t.Fatalf("feedTitle = %q", a.FeedTitle)
	}
}

func TestListFiltersAndPagination(t *testing.T) {
	s := openTest(t)
	c, _ := s.CreateCategory("Cat")
	f1, _ := s.CreateFeed(&Feed{URL: "https://a/rss", Title: "A", CategoryID: &c.ID})
	f2, _ := s.CreateFeed(&Feed{URL: "https://b/rss", Title: "B"})
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seedArticles(t, s, f1.ID, 7, base)
	seedArticles(t, s, f2.ID, 5, base.Add(time.Hour))

	all, _, _ := s.ListArticles(ArticleQuery{})
	if len(all) != 12 {
		t.Fatalf("all = %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].PublishedAt.After(all[i-1].PublishedAt) {
			t.Fatalf("not newest first")
		}
	}
	if all[0].Content != "" {
		t.Fatalf("list should omit content")
	}
	byFeed, _, _ := s.ListArticles(ArticleQuery{FeedID: &f1.ID})
	if len(byFeed) != 7 {
		t.Fatalf("byFeed = %d", len(byFeed))
	}
	byCat, _, _ := s.ListArticles(ArticleQuery{CategoryID: &c.ID})
	if len(byCat) != 7 {
		t.Fatalf("byCat = %d", len(byCat))
	}
	// pagination with no gaps or dups
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		page, next, err := s.ListArticles(ArticleQuery{Cursor: cursor, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, a := range page {
			if seen[a.ID] {
				t.Fatalf("dup id %d", a.ID)
			}
			seen[a.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 12 || pages != 3 {
		t.Fatalf("seen %d over %d pages", len(seen), pages)
	}
	if _, _, err := s.ListArticles(ArticleQuery{Cursor: "!!"}); err == nil {
		t.Fatalf("bad cursor should error")
	}
	// read / starred
	if err := s.SetRead([]int64{all[0].ID, all[1].ID}, true); err != nil {
		t.Fatal(err)
	}
	unread, _, _ := s.ListArticles(ArticleQuery{Status: StatusUnread})
	if len(unread) != 10 {
		t.Fatalf("unread = %d", len(unread))
	}
	s.SetStarred(all[3].ID, true)
	starred, _, _ := s.ListArticles(ArticleQuery{Status: StatusStarred})
	if len(starred) != 1 || starred[0].ID != all[3].ID {
		t.Fatalf("starred = %+v", starred)
	}
	counts, _ := s.Counts()
	if counts.Total != 10 || counts.Starred != 1 || counts.Feeds[fmt.Sprint(f2.ID)] != 3 || counts.Categories[fmt.Sprint(c.ID)] != 7 {
		t.Fatalf("counts = %+v", counts)
	}
	// mark all read scoped
	n, err := s.MarkAllRead(&f1.ID, nil, nil)
	if err != nil || n != 7 {
		t.Fatalf("MarkAllRead feed: %d %v", n, err)
	}
	cutoff := base.Add(time.Hour)
	n, _ = s.MarkAllRead(nil, nil, &cutoff)
	if n != 1 {
		t.Fatalf("older-than should hit only the oldest unread; got %d", n)
	}
	counts, _ = s.Counts()
	if counts.Total != 2 {
		t.Fatalf("total after mark = %d", counts.Total)
	}
	s.SetRead([]int64{all[0].ID}, false)
	counts, _ = s.Counts()
	if counts.Total != 3 {
		t.Fatalf("unread toggle back = %d", counts.Total)
	}
}

func TestSearch(t *testing.T) {
	s := openTest(t)
	f, _ := s.CreateFeed(&Feed{URL: "https://a/rss", Title: "A"})
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s.UpsertArticles(f.ID, []Article{
		{GUID: "1", Title: "Kernel release", Content: "<p>Linux 7.3 is out</p>", PublishedAt: base},
		{GUID: "2", Title: "Cooking", Content: "<p>Bread recipe</p>", PublishedAt: base},
	})
	res, _, err := s.ListArticles(ArticleQuery{Query: "linux"})
	if err != nil || len(res) != 1 || res[0].Title != "Kernel release" {
		t.Fatalf("search: %+v %v", res, err)
	}
	res, _, _ = s.ListArticles(ArticleQuery{Query: "kern"})
	if len(res) != 1 {
		t.Fatalf("prefix search failed")
	}
	res, _, err = s.ListArticles(ArticleQuery{Query: `bread "quoted`})
	if err != nil || len(res) != 0 {
		t.Fatalf("odd quote should not error: %v", err)
	}
	if err := s.DeleteFeed(f.ID); err != nil {
		t.Fatal(err)
	}
	res, _, _ = s.ListArticles(ArticleQuery{Query: "linux"})
	if len(res) != 0 {
		t.Fatalf("fts not cleaned after cascade delete")
	}
}

func TestImageURLStoredAndBackfilled(t *testing.T) {
	s := openTest(t)
	f, _ := s.CreateFeed(&Feed{URL: "https://x.test/rss"})
	if _, err := s.UpsertArticles(f.ID, []Article{{GUID: "a", Title: "A", URL: "https://x.test/a"}}); err != nil {
		t.Fatal(err)
	}
	// The feed later adds a thumbnail for the same item: backfilled without touching state.
	if err := s.SetRead([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	n, err := s.UpsertArticles(f.ID, []Article{{GUID: "a", Title: "A changed", URL: "https://x.test/a", ImageURL: "https://x.test/a.jpg"}, {GUID: "b", URL: "https://x.test/b", ImageURL: "https://x.test/b.jpg"}})
	if err != nil || n != 1 {
		t.Fatalf("second upsert: n=%d err=%v", n, err)
	}
	items, _, err := s.ListArticles(ArticleQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Article{}
	for _, a := range items {
		got[a.URL] = a
	}
	if got["https://x.test/a"].ImageURL != "https://x.test/a.jpg" || got["https://x.test/a"].Title != "A" || !got["https://x.test/a"].Read {
		t.Fatalf("backfill: %+v", got["https://x.test/a"])
	}
	if got["https://x.test/b"].ImageURL != "https://x.test/b.jpg" {
		t.Fatalf("insert: %+v", got["https://x.test/b"])
	}
	one, _ := s.GetArticle(got["https://x.test/a"].ID)
	if one.ImageURL != "https://x.test/a.jpg" {
		t.Fatalf("get: %+v", one)
	}
}

func TestMigrateAddsImageURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE articles DROP COLUMN image_url"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen should migrate: %v", err)
	}
	defer s.Close()
	if ok, _ := hasColumn(s.db, "articles", "image_url"); !ok {
		t.Fatal("image_url column not added")
	}
}
