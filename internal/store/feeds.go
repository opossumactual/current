package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// MaxFetchIntervalMin caps the error backoff.
const MaxFetchIntervalMin = 360

// Feed is a subscription.
type Feed struct {
	ID               int64      `json:"id"`
	CategoryID       *int64     `json:"categoryId"`
	Title            string     `json:"title"`
	URL              string     `json:"url"`
	SiteURL          string     `json:"siteUrl"`
	Description      string     `json:"description"`
	IconURL          string     `json:"iconUrl"`
	ETag             string     `json:"-"`
	LastModified     string     `json:"-"`
	LastFetchedAt    *time.Time `json:"lastFetchedAt"`
	LastSuccessAt    *time.Time `json:"lastSuccessAt"`
	NextFetchAt      time.Time  `json:"nextFetchAt"`
	Paused           bool       `json:"paused"`
	Unsubscribed     bool       `json:"unsubscribed"`
	LastError        string     `json:"lastError"`
	ErrorCount       int        `json:"-"`
	FetchIntervalMin int        `json:"fetchIntervalMin"`
	ExtractFulltext  bool       `json:"extractFulltext"`
	CreatedAt        time.Time  `json:"createdAt"`
	Unread           int        `json:"unread"`
}

const feedCols = `f.id, f.category_id, f.title, f.url, f.site_url, f.description, f.icon_url, f.etag, f.last_modified,
	f.last_fetched_at, f.next_fetch_at, f.last_error, f.error_count, f.fetch_interval_min, f.extract_fulltext, f.created_at,
	(SELECT count(*) FROM articles a WHERE a.feed_id = f.id AND a.read = 0), f.paused, f.unsubscribed, f.last_success_at`

type scanner interface{ Scan(dest ...any) error }

func scanFeed(r scanner) (*Feed, error) {
	var f Feed
	var cat sql.NullInt64
	var lastFetched sql.NullInt64
	var next, created int64
	var extract int
	var paused, unsubscribed int
	var success sql.NullInt64
	err := r.Scan(&f.ID, &cat, &f.Title, &f.URL, &f.SiteURL, &f.Description, &f.IconURL, &f.ETag, &f.LastModified,
		&lastFetched, &next, &f.LastError, &f.ErrorCount, &f.FetchIntervalMin, &extract, &created, &f.Unread, &paused, &unsubscribed, &success)
	if err != nil {
		return nil, err
	}
	if cat.Valid {
		v := cat.Int64
		f.CategoryID = &v
	}
	if lastFetched.Valid {
		t := time.Unix(lastFetched.Int64, 0).UTC()
		f.LastFetchedAt = &t
	}
	f.NextFetchAt = time.Unix(next, 0).UTC()
	f.CreatedAt = time.Unix(created, 0).UTC()
	f.ExtractFulltext = extract == 1
	f.Paused = paused == 1
	f.Unsubscribed = unsubscribed == 1
	if success.Valid {
		t := time.Unix(success.Int64, 0).UTC()
		f.LastSuccessAt = &t
	}
	return &f, nil
}

// ListFeeds returns all feeds ordered by title.
func (s *Store) ListFeeds() ([]Feed, error) {
	return s.listFeeds(false)
}

func (s *Store) ListAllFeeds() ([]Feed, error) { return s.listFeeds(true) }

func (s *Store) listFeeds(includeArchived bool) ([]Feed, error) {
	where := " WHERE f.unsubscribed = 0"
	if includeArchived {
		where = ""
	}
	rows, err := s.db.Query(`SELECT ` + feedCols + ` FROM feeds f` + where + ` ORDER BY lower(f.title), f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Feed{}
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// GetFeed returns one feed.
func (s *Store) GetFeed(id int64) (*Feed, error) {
	f, err := scanFeed(s.db.QueryRow(`SELECT `+feedCols+` FROM feeds f WHERE f.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// GetFeedByURL returns the feed subscribed at url.
func (s *Store) GetFeedByURL(url string) (*Feed, error) {
	f, err := scanFeed(s.db.QueryRow(`SELECT `+feedCols+` FROM feeds f WHERE f.url = ?`, url))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// CreateFeed inserts a feed. URL must be unique.
func (s *Store) CreateFeed(f *Feed) (*Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.FetchIntervalMin <= 0 {
		f.FetchIntervalMin = 60
	}
	now := time.Now().UTC()
	var cat any
	if f.CategoryID != nil {
		cat = *f.CategoryID
	}
	res, err := s.db.Exec(`INSERT INTO feeds(category_id, title, url, site_url, description, icon_url, fetch_interval_min, extract_fulltext, created_at, next_fetch_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		cat, f.Title, f.URL, f.SiteURL, f.Description, f.IconURL, f.FetchIntervalMin, boolInt(f.ExtractFulltext), now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeed(id)
}

// FeedUpdate carries optional field updates.
type FeedUpdate struct {
	URL              *string
	Paused           *bool
	Unsubscribed     *bool
	Title            *string
	CategoryID       **int64 // outer nil = unchanged, inner nil = clear
	ExtractFulltext  *bool
	FetchIntervalMin *int
	SiteURL          *string
	IconURL          *string
	Description      *string
}

// UpdateFeed applies the non-nil fields of u.
func (s *Store) UpdateFeed(id int64, u FeedUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sets := []string{}
	args := []any{}
	if u.URL != nil {
		sets = append(sets, "url = ?", "etag = ''", "last_modified = ''", "next_fetch_at = 0")
		args = append(args, *u.URL)
	}
	if u.Paused != nil && !*u.Paused {
		sets = append(sets, "next_fetch_at = 0")
	}
	if u.Unsubscribed != nil && !*u.Unsubscribed {
		sets = append(sets, "next_fetch_at = 0")
	}
	if u.Paused != nil {
		sets = append(sets, "paused = ?")
		args = append(args, boolInt(*u.Paused))
	}
	if u.Unsubscribed != nil {
		sets = append(sets, "unsubscribed = ?")
		args = append(args, boolInt(*u.Unsubscribed))
	}
	if u.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *u.Title)
	}
	if u.CategoryID != nil {
		sets = append(sets, "category_id = ?")
		if *u.CategoryID == nil {
			args = append(args, nil)
		} else {
			args = append(args, **u.CategoryID)
		}
	}
	if u.ExtractFulltext != nil {
		sets = append(sets, "extract_fulltext = ?")
		args = append(args, boolInt(*u.ExtractFulltext))
	}
	if u.FetchIntervalMin != nil {
		sets = append(sets, "fetch_interval_min = ?", "next_fetch_at = 0")
		args = append(args, *u.FetchIntervalMin)
	}
	if u.SiteURL != nil {
		sets = append(sets, "site_url = ?")
		args = append(args, *u.SiteURL)
	}
	if u.IconURL != nil {
		sets = append(sets, "icon_url = ?")
		args = append(args, *u.IconURL)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if len(sets) == 0 {
		_, err := s.GetFeed(id)
		return err
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE feeds SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UnsubscribeFeed stops future polling and retains all existing article state.
func (s *Store) UnsubscribeFeed(id int64) error {
	yes := true
	return s.UpdateFeed(id, FeedUpdate{Unsubscribed: &yes})
}

// DeleteFeed removes a feed and its articles.
func (s *Store) DeleteFeed(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM feeds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FeedsDue returns feeds whose next fetch time has passed.
func (s *Store) FeedsDue(now time.Time) ([]Feed, error) {
	rows, err := s.db.Query(`SELECT `+feedCols+` FROM feeds f WHERE f.next_fetch_at <= ? AND f.paused = 0 AND f.unsubscribed = 0 ORDER BY f.next_fetch_at`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Feed{}
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// MarkFetched records the outcome of a fetch and schedules the next one.
// On error the interval used for scheduling doubles per consecutive failure, capped
// at MaxFetchIntervalMin; the configured fetch_interval_min is not changed.
func (s *Store) MarkFetched(id int64, etag, lastModified string, fetchErr error, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var interval, errCount int
	if err := s.db.QueryRow(`SELECT fetch_interval_min, error_count FROM feeds WHERE id = ?`, id).Scan(&interval, &errCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	msg := ""
	if fetchErr != nil {
		errCount++
		msg = fetchErr.Error()
		for i := 0; i < errCount; i++ {
			interval *= 2
			if interval >= MaxFetchIntervalMin {
				interval = MaxFetchIntervalMin
				break
			}
		}
	} else {
		errCount = 0
	}
	next := now.Add(time.Duration(interval) * time.Minute)
	_, err := s.db.Exec(`UPDATE feeds SET etag = ?, last_modified = ?, last_fetched_at = ?, next_fetch_at = ?, last_error = ?, error_count = ?, last_success_at = CASE WHEN ? = '' THEN ? ELSE last_success_at END WHERE id = ?`,
		etag, lastModified, now.Unix(), next.Unix(), msg, errCount, msg, now.Unix(), id)
	return err
}

// ScheduleNow makes a feed due immediately.
func (s *Store) ScheduleNow(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE feeds SET next_fetch_at = 0 WHERE id = ?`, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SubscribeFeed restores a previous subscription without losing reading history.
func (s *Store) SubscribeFeed(f *Feed) (*Feed, error) {
	old, err := s.GetFeedByURL(f.URL)
	if err == nil {
		if !old.Unsubscribed {
			return nil, ErrDuplicate
		}
		no := false
		u := FeedUpdate{Unsubscribed: &no, Paused: &no}
		if f.Title != "" {
			u.Title = &f.Title
		}
		if f.CategoryID != nil {
			u.CategoryID = &f.CategoryID
		}
		if err = s.UpdateFeed(old.ID, u); err != nil {
			return nil, err
		}
		s.ScheduleNow(old.ID)
		return s.GetFeed(old.ID)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.CreateFeed(f)
}

// ScheduleAll queues active subscriptions for the bounded scheduler worker pool.
func (s *Store) ScheduleAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE feeds SET next_fetch_at=0 WHERE paused=0 AND unsubscribed=0`)
	return err
}
