// Package store is the SQLite persistence layer.
//
// No method infers a current library. There is no ambient "current library"
// value anywhere, and nothing falls back to "the only one" — DESIGN.md §10
// requires that the v1 single-library build never assume a singleton, so the
// host layer is reachable later without a refactor.
//
// Three shapes of method follow from that, and only the third takes a
// boulevard.LibraryID:
//
//   - Resolution boundaries are handed an identifier and return what it
//     names, including which library that is: LibraryBySlug, LibraryByID,
//     TokenBySecret, SessionByID, and DeleteSession. Resolving the library
//     is their job, so they cannot be given one.
//   - Host-scoped queries ask about the host rather than a shelf, so a
//     library identifier would be meaningless on them: LibrarySlugs.
//   - Everything else takes an explicit boulevard.LibraryID, and where it
//     also takes something already carrying one — RecordScan — it rejects
//     the pair if they disagree.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // pure-Go driver; cgo would break the static binary
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

// FileMode is what the database and its sidecars are kept at.
//
// Token secrets are stored in plaintext (DESIGN.md §4), and a secret IS the
// write credential for the shelf. At 0644 any local account on a shared
// host could read them and gain leave/take rights without ever standing at
// the box — which is the whole premise. Owner-only, always.
const FileMode = 0o600

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
	// After the schema, so the WAL and shared-memory sidecars exist and get
	// restricted too: they hold the same secrets as the database proper.
	if err := restrict(path); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// restrict narrows the database and its sidecars to owner-only. A failure
// here is fatal on purpose: continuing would leave every token secret
// readable by every account on the host, which is the one outcome the
// plaintext storage decision cannot survive.
func restrict(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		err := os.Chmod(p, FileMode)
		if errors.Is(err, os.ErrNotExist) {
			continue // no WAL yet, or already checkpointed away
		}
		if err != nil {
			return fmt.Errorf("restrict %q to %#o: %w (it holds token secrets in plaintext)", p, FileMode, err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }
