package store

import (
	"database/sql"
	"errors"
)

// Category groups feeds.
type Category struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	Unread   int    `json:"unread"`
}

// ListCategories returns categories ordered by position then title.
func (s *Store) ListCategories() ([]Category, error) {
	rows, err := s.db.Query(`SELECT c.id, c.title, c.position,
		(SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE f.category_id = c.id AND a.read = 0)
		FROM categories c ORDER BY c.position, c.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Title, &c.Position, &c.Unread); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Category{}
	}
	return out, rows.Err()
}

// GetCategory returns one category.
func (s *Store) GetCategory(id int64) (*Category, error) {
	var c Category
	err := s.db.QueryRow(`SELECT id, title, position FROM categories WHERE id = ?`, id).Scan(&c.ID, &c.Title, &c.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// CreateCategory inserts a category and returns it.
func (s *Store) CreateCategory(title string) (*Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO categories(title, position) VALUES (?, (SELECT coalesce(max(position),0)+1 FROM categories))`, title)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCategory(id)
}

// FindOrCreateCategory returns the category with the given title, creating it if needed.
func (s *Store) FindOrCreateCategory(title string) (*Category, error) {
	var c Category
	err := s.db.QueryRow(`SELECT id, title, position FROM categories WHERE title = ?`, title).Scan(&c.ID, &c.Title, &c.Position)
	if err == nil {
		return &c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return s.CreateCategory(title)
}

// UpdateCategory renames and/or repositions a category.
func (s *Store) UpdateCategory(id int64, title string, position int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE categories SET title = ?, position = ? WHERE id = ?`, title, position, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCategory removes a category; its feeds become uncategorized.
func (s *Store) DeleteCategory(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
