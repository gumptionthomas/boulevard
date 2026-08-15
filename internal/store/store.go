// Package store is the SQLite persistence layer.
//
// No method infers a current library. There is no ambient "current library"
// value anywhere, and nothing falls back to "the only one" — DESIGN.md §10
// requires that the v1 single-library build never assume a singleton, so the
// host layer is reachable later without a refactor.
//
// Four shapes of method follow from that, and only the third and fourth
// take a boulevard.LibraryID:
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
//   - Session-scoped queries ask about one session's own takes rather than
//     a shelf: TakenBySession and TakenToZeroBySession. TakenBySession
//     takes no LibraryID, for the same reason a resolution boundary does
//     not — a session id already names a single session, which already
//     names its library, so a second identifier could only disagree with
//     the first. TakenToZeroBySession takes both a LibraryID and a
//     SessionID, because unlike TakenBySession it joins onto items and
//     returns Item values, so the library scope items themselves require
//     applies here too; the session id is still what keeps the result to
//     that one session's own takes, never another session's, which is the
//     hard requirement §5's "consumption is global, mutation is local"
//     puts on this specific query.
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

// ErrNotPending is a row that exists but has already been decided.
//
// Separate from ErrNotFound because the two need different answers: a
// prefix that names nothing is a typo the steward should retype, while an
// item that is already shelved or released is a command that has already
// been run. Both are the steward's mistake rather than the database's, and
// the CLI reports them as usage errors.
var ErrNotPending = errors.New("not pending")

// ErrLimitReached is the per-session rate limit (DESIGN.md §4: 3 leaves,
// 3 takes). Presence is attestable, not enforceable — scanning again
// mints a new session with fresh counters, and that is not a loophole to
// close.
var ErrLimitReached = errors.New("session limit reached")

// ErrNoCopiesLeft guards a shelved item with no copies. An item that
// reaches zero sheds in the same transaction, so this should be
// unreachable — except for a steward who sets default_copies = 0, which
// shelves items with nothing to take. Not dead code.
var ErrNoCopiesLeft = errors.New("no copies left")

// ErrShelfFull is undo and re-shelve meeting a full shelf. Neither
// evicts to make room: a stray tap must not cost a different item its
// place, and eviction is FIFO precisely so nobody's behaviour reorders
// the shelf.
var ErrShelfFull = errors.New("shelf is full")

// ErrNotShed distinguishes "no such item" from "that item is not in the
// shed", the way ErrNotPending does for the approval queue. A steward
// needs to tell a mistyped handle from a second `reshelve` on the same
// item.
var ErrNotShed = errors.New("item is not in the shed")

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

// Open opens or creates the database and applies pending migrations.
func Open(path string) (*Store, error) {
	db, err := openRaw(path)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database %q: %w", path, err)
	}
	// After migrating, so the WAL and shared-memory sidecars exist and get
	// restricted too: they hold the same secrets as the database proper.
	if err := restrict(path); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// openRaw opens the database with this package's pragmas but applies no
// migrations. Tests use it to build a database as an earlier version left it.
func openRaw(path string) (*sql.DB, error) {
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
	return db, nil
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
