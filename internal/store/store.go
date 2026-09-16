// Package store persists feeds and articles in SQLite.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrDuplicate is returned when a unique constraint is violated.
var ErrDuplicate = errors.New("already exists")

// Store wraps a SQLite database.
type Store struct {
	db *sql.DB
	mu sync.Mutex // serializes writes
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(wal)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate adds columns introduced after the first release to existing databases.
// schema.sql already carries them for fresh databases, so each step is a no-op there.
func migrate(db *sql.DB) error {
	added := []struct{ table, column, ddl string }{
		{"articles", "image_url", "ALTER TABLE articles ADD COLUMN image_url TEXT NOT NULL DEFAULT ''"},
		{"articles", "fulltext", "ALTER TABLE articles ADD COLUMN fulltext TEXT NOT NULL DEFAULT ''"},
		{"feeds", "paused", "ALTER TABLE feeds ADD COLUMN paused INTEGER NOT NULL DEFAULT 0"},
		{"feeds", "unsubscribed", "ALTER TABLE feeds ADD COLUMN unsubscribed INTEGER NOT NULL DEFAULT 0"},
		{"feeds", "last_success_at", "ALTER TABLE feeds ADD COLUMN last_success_at INTEGER"},
	}
	for _, m := range added {
		ok, err := hasColumn(db, m.table, m.column)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := db.Exec(m.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
