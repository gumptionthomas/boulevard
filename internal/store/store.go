// Package store is the SQLite persistence layer.
//
// Every method takes a library identifier explicitly. There is no ambient
// "current library" value anywhere — DESIGN.md §10 requires that the v1
// single-library build never assume a singleton, so the host layer is
// reachable later without a refactor.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver; cgo would break the static binary
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

// Open opens or creates the database and applies the schema.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	// A single connection for writes. Concurrent writers on SQLite produce
	// SQLITE_BUSY, which DESIGN.md §2 names as a known hazard.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
