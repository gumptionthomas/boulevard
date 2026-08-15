package store

import (
	"database/sql"
	"fmt"
)

// A migration is a numbered batch of statements applied exactly once.
//
// schema.sql is migration 1 and must stay as it is: it is all
// CREATE TABLE IF NOT EXISTS, so re-running it on a database that predates
// this mechanism is a no-op. Later migrations add to it with ALTER TABLE.
// Never add a column to schema.sql as well as to a migration — a fresh
// database would then run both and fail on "duplicate column name".
type migration struct {
	version int
	stmts   string
}

// addLibrarySettings gives libraries the §3 fields the first schema lacked.
// The defaults are DESIGN.md §3's: a twelve-slot shelf, ninety days, three
// copies, and approval on.
const addLibrarySettings = `
ALTER TABLE libraries ADD COLUMN slots             INTEGER NOT NULL DEFAULT 12;
ALTER TABLE libraries ADD COLUMN max_age_days      INTEGER NOT NULL DEFAULT 90;
ALTER TABLE libraries ADD COLUMN default_copies    INTEGER NOT NULL DEFAULT 3;
ALTER TABLE libraries ADD COLUMN approval_required INTEGER NOT NULL DEFAULT 1;
ALTER TABLE libraries ADD COLUMN steward_contact   TEXT    NOT NULL DEFAULT '';
`

// createItems adds the shelf itself.
//
// The index is not the dead weight removed from tokens in Milestone 0: the
// shelf query and the eviction query both filter by library and state and
// order by shelved_at, and no existing constraint provides that.
const createItems = `
CREATE TABLE IF NOT EXISTS items (
    id           TEXT PRIMARY KEY,
    library_id   TEXT NOT NULL REFERENCES libraries(id),
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL,
    note         TEXT NOT NULL,
    attribution  TEXT NOT NULL DEFAULT '',
    copies_total INTEGER NOT NULL DEFAULT 0,
    copies_left  INTEGER NOT NULL DEFAULT 0,
    state        TEXT NOT NULL,
    pinned       INTEGER NOT NULL DEFAULT 0,
    views        INTEGER NOT NULL DEFAULT 0,
    takes        INTEGER NOT NULL DEFAULT 0,
    left_at      TEXT NOT NULL,
    shelved_at   TEXT,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_shelf
    ON items(library_id, state, shelved_at);
`

var migrations = []migration{
	{1, schema},
	{2, addLibrarySettings},
	{3, createItems},
}

// migrate applies every migration not yet recorded, each in its own
// transaction so a failure leaves the database at the last good version
// rather than half-way through one.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	if err := refuseNewerSchema(db); err != nil {
		return err
	}

	for _, m := range migrations {
		var done int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&done); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if done == 1 {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.stmts); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`,
			m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

// refuseNewerSchema stops an older binary from running against a database a
// newer one has already migrated.
//
// The binary is a single file a steward downloads, so rolling back to a
// previous release is a plausible thing to do — and this loop is otherwise
// happy to skip every recorded migration and carry on against a schema it
// has never seen. That surfaces as mysterious errors from individual
// queries, at some later moment, with nothing pointing at the cause.
// Naming both numbers turns it into one sentence a steward can act on.
func refuseNewerSchema(db *sql.DB) error {
	var found int
	if err := db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&found); err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	known := migrations[len(migrations)-1].version
	if found > known {
		return fmt.Errorf(
			"database is at schema version %d but this build only knows %d; run a newer boulevard",
			found, known)
	}
	return nil
}
