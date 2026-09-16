package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Article is one feed entry.
type Article struct {
	ID        int64  `json:"id"`
	FeedID    int64  `json:"feedId"`
	FeedTitle string `json:"feedTitle,omitempty"`
	GUID      string `json:"-"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Content   string `json:"content,omitempty"`
	// Fulltext is the readability-extracted page body, loaded on demand and cached.
	Fulltext    string    `json:"fulltext,omitempty"`
	Summary     string    `json:"summary"`
	ImageURL    string    `json:"imageUrl"`
	PublishedAt time.Time `json:"publishedAt"`
	FetchedAt   time.Time `json:"fetchedAt"`
	Read        bool      `json:"read"`
	Starred     bool      `json:"starred"`
}

// Status filters article lists.
type Status string

const (
	StatusAll     Status = "all"
	StatusUnread  Status = "unread"
	StatusStarred Status = "starred"
)

// ArticleQuery selects articles for ListArticles.
type ArticleQuery struct {
	FeedID     *int64
	CategoryID *int64
	Status     Status
	Query      string
	Cursor     string
	Limit      int
}

// UpsertArticles inserts new articles for a feed, ignoring ones whose GUID already exists.
// It returns the number inserted. Existing rows keep their content and state; only a
// missing image_url is backfilled when the feed now supplies one.
func (s *Store) UpsertArticles(feedID int64, items []Article) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO articles(feed_id, guid, url, title, author, content, summary, image_url, published_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	fill, err := tx.Prepare(`UPDATE articles SET image_url = ? WHERE feed_id = ? AND guid = ? AND image_url = ''`)
	if err != nil {
		return 0, err
	}
	defer fill.Close()
	now := time.Now().UTC()
	inserted := 0
	for _, a := range items {
		pub := a.PublishedAt
		if pub.IsZero() {
			pub = now
		}
		fetched := a.FetchedAt
		if fetched.IsZero() {
			fetched = now
		}
		res, err := stmt.Exec(feedID, a.GUID, a.URL, a.Title, a.Author, a.Content, a.Summary, a.ImageURL, pub.Unix(), fetched.Unix())
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		} else if a.ImageURL != "" {
			if _, err := fill.Exec(a.ImageURL, feedID, a.GUID); err != nil {
				return inserted, err
			}
		}
	}
	return inserted, tx.Commit()
}

const articleListCols = `a.id, a.feed_id, f.title, a.url, a.title, a.author, a.summary, a.image_url, a.published_at, a.fetched_at, a.read, a.starred`

func scanArticleList(r scanner) (*Article, error) {
	var a Article
	var pub, fetched int64
	var read, starred int
	if err := r.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.URL, &a.Title, &a.Author, &a.Summary, &a.ImageURL, &pub, &fetched, &read, &starred); err != nil {
		return nil, err
	}
	a.PublishedAt = time.Unix(pub, 0).UTC()
	a.FetchedAt = time.Unix(fetched, 0).UTC()
	a.Read = read == 1
	a.Starred = starred == 1
	return &a, nil
}

func encodeCursor(pub int64, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(pub, 10) + "|" + strconv.FormatInt(id, 10)))
}

func decodeCursor(c string) (pub, id int64, err error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	pub, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	return pub, id, nil
}

// ListArticles returns a page of articles (without content) newest first and the cursor for the next page.
func (s *Store) ListArticles(q ArticleQuery) ([]Article, string, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := []string{"1=1"}
	args := []any{}
	if q.FeedID != nil {
		where = append(where, "a.feed_id = ?")
		args = append(args, *q.FeedID)
	}
	if q.CategoryID != nil {
		where = append(where, "f.category_id = ?")
		args = append(args, *q.CategoryID)
	}
	switch q.Status {
	case StatusUnread:
		where = append(where, "a.read = 0")
	case StatusStarred:
		where = append(where, "a.starred = 1")
	}
	join := ""
	if strings.TrimSpace(q.Query) != "" {
		join = " JOIN articles_fts fts ON fts.rowid = a.id"
		where = append(where, "articles_fts MATCH ?")
		args = append(args, ftsQuery(q.Query))
	}
	if q.Cursor != "" {
		pub, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(a.published_at < ? OR (a.published_at = ? AND a.id < ?))")
		args = append(args, pub, pub, id)
	}
	args = append(args, limit+1)
	rows, err := s.db.Query(`SELECT `+articleListCols+` FROM articles a JOIN feeds f ON f.id = a.feed_id`+join+
		` WHERE `+strings.Join(where, " AND ")+` ORDER BY a.published_at DESC, a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Article{}
	for rows.Next() {
		a, err := scanArticleList(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = encodeCursor(last.PublishedAt.Unix(), last.ID)
	}
	return out, next, nil
}

// ftsQuery turns free text into a safe FTS5 query: each token quoted, prefix match, AND-ed.
func ftsQuery(q string) string {
	var parts []string
	for _, tok := range strings.Fields(q) {
		tok = strings.ReplaceAll(tok, `"`, `""`)
		parts = append(parts, `"`+tok+`"*`)
	}
	return strings.Join(parts, " AND ")
}

// GetArticle returns one article with content.
func (s *Store) GetArticle(id int64) (*Article, error) {
	row := s.db.QueryRow(`SELECT `+articleListCols+`, a.content, a.fulltext FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.id = ?`, id)
	var a Article
	var pub, fetched int64
	var read, starred int
	err := row.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.URL, &a.Title, &a.Author, &a.Summary, &a.ImageURL, &pub, &fetched, &read, &starred, &a.Content, &a.Fulltext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.PublishedAt = time.Unix(pub, 0).UTC()
	a.FetchedAt = time.Unix(fetched, 0).UTC()
	a.Read = read == 1
	a.Starred = starred == 1
	return &a, nil
}

// SetFulltext caches the extracted page body for an article.
func (s *Store) SetFulltext(id int64, html string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE articles SET fulltext = ? WHERE id = ?`, html, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRead marks articles read or unread.
func (s *Store) SetRead(ids []int64, read bool) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{boolInt(read)}
	var readAt any
	if read {
		readAt = time.Now().UTC().Unix()
	}
	args = append(args, readAt)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.Exec(`UPDATE articles SET read = ?, read_at = ? WHERE id IN (`+ph+`)`, args...)
	return err
}

// SetStarred stars or unstars an article.
func (s *Store) SetStarred(id int64, starred bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE articles SET starred = ? WHERE id = ?`, boolInt(starred), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks unread articles read, optionally scoped to a feed or category and
// to articles published at or before olderThan. It returns the number changed.
func (s *Store) MarkAllRead(feedID, categoryID *int64, olderThan *time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	where := []string{"read = 0"}
	args := []any{time.Now().UTC().Unix()}
	if feedID != nil {
		where = append(where, "feed_id = ?")
		args = append(args, *feedID)
	}
	if categoryID != nil {
		where = append(where, "feed_id IN (SELECT id FROM feeds WHERE category_id = ?)")
		args = append(args, *categoryID)
	}
	if olderThan != nil {
		where = append(where, "published_at <= ?")
		args = append(args, olderThan.Unix())
	}
	res, err := s.db.Exec(`UPDATE articles SET read = 1, read_at = ? WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Counts summarizes unread and starred totals.
type Counts struct {
	Total      int            `json:"total"`
	Starred    int            `json:"starred"`
	Feeds      map[string]int `json:"feeds"`
	Categories map[string]int `json:"categories"`
}

// Counts returns unread counts per feed and category plus totals.
func (s *Store) Counts() (*Counts, error) {
	c := &Counts{Feeds: map[string]int{}, Categories: map[string]int{}}
	rows, err := s.db.Query(`SELECT a.feed_id, f.category_id, count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.read = 0 GROUP BY a.feed_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fid int64
		var cid sql.NullInt64
		var n int
		if err := rows.Scan(&fid, &cid, &n); err != nil {
			return nil, err
		}
		c.Feeds[strconv.FormatInt(fid, 10)] = n
		c.Total += n
		if cid.Valid {
			c.Categories[strconv.FormatInt(cid.Int64, 10)] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM articles WHERE starred = 1`).Scan(&c.Starred); err != nil {
		return nil, err
	}
	return c, nil
}

// PruneRead deletes read, unstarred articles older than the given time. Returns rows deleted.
func (s *Store) PruneRead(olderThan time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM articles WHERE read = 1 AND starred = 0 AND published_at < ?`, olderThan.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ArticleExists reports whether a feed already has an article with the GUID.
func (s *Store) ArticleExists(feedID int64, guid string) bool {
	var n int
	s.db.QueryRow(`SELECT count(*) FROM articles WHERE feed_id = ? AND guid = ?`, feedID, guid).Scan(&n)
	return n > 0
}
