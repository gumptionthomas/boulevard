package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// exportedTables are copied in dependency order: items and tokens both
// carry a foreign key to libraries, and _pragma=foreign_keys(1) is on.
//
// What is missing matters as much as what is here. sessions,
// steward_sessions and session_takes are deliberately not copied. Durable
// library state travels; ephemeral presence state does not. Copying the two
// session tables would hand working credentials to whoever holds the file,
// and copying session_takes would move exactly the durable "who took what"
// record that table's swept-with-the-session design exists to avoid being —
// the same reasoning that keeps a session id out of the request log and out
// of a left item.
//
// libraries.steward_key_hash does travel: DESIGN.md §10's story is a host
// who loses interest handing out twelve files and dissolving cleanly, and
// each of those files goes to the steward whose key that hash already is.
var exportedTables = []struct {
	name    string
	columns string
	where   string
}{
	{"libraries", "id, slug, name, location_label, base_url, created_at, slots, max_age_days, default_copies, approval_required, steward_contact, steward_key_hash", "id = ?"},
	{"tokens", "id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at, created_at", "library_id = ?"},
	{"items", "id, library_id, type, payload, note, attribution, copies_total, copies_left, state, pinned, views, takes, left_at, shelved_at, created_at, shed_at, shed_reason", "library_id = ?"},
}

// CopyLibraryTo writes one library to its own SQLite file at path.
//
// The target is created through Open, which applies the same migrations and
// restricts the file to 0600 — the export carries every card's secret, and
// a secret is the shelf's write credential (DESIGN.md §4). Same schema in,
// same schema out: `boulevard serve --db <path>` runs the result unchanged,
// which is what makes ejection a file copy rather than a format (§10), and
// makes v2's move to one-database-per-library a no-op rather than a
// migration.
//
// Rows are read fully into memory before the target is written. The source
// runs with SetMaxOpenConns(1), so holding a *sql.Rows open across another
// query on it would deadlock rather than error. Reading everything first is
// safe here because both the shelf (capped at `slots`) and the booklet
// (always twelve cards) are bounded by design.
func (s *Store) CopyLibraryTo(ctx context.Context, id boulevard.LibraryID, path string) error {
	if _, err := s.LibraryByID(ctx, id); err != nil {
		return err
	}

	dst, err := Open(path)
	if err != nil {
		return fmt.Errorf("create export at %q: %w", path, err)
	}
	defer dst.Close()

	tx, err := dst.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin export: %w", err)
	}
	defer tx.Rollback()

	for _, tbl := range exportedTables {
		rows, err := s.readTable(ctx, tbl.name, tbl.columns, tbl.where, string(id))
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		placeholders := "?" + strings.Repeat(", ?", len(rows[0])-1)
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO `+tbl.name+` (`+tbl.columns+`) VALUES (`+placeholders+`)`)
		if err != nil {
			return fmt.Errorf("prepare %s insert: %w", tbl.name, err)
		}
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, r...); err != nil {
				stmt.Close()
				return fmt.Errorf("copy %s row: %w", tbl.name, err)
			}
		}
		stmt.Close()
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit export: %w", err)
	}
	return nil
}

// readTable drains one table's matching rows into memory. Values come back
// as []any of whatever the driver produced, which is exactly what the
// matching INSERT wants — no per-table struct, and no column list to keep
// in sync in two places.
//
// It drains before returning rather than streaming. The source runs with
// SetMaxOpenConns(1), so holding a *sql.Rows open while the caller issues
// another query on the same handle would deadlock rather than error. Both
// the shelf (capped at `slots`) and the booklet (twelve cards) are bounded
// by design, so there is no unbounded read to worry about.
func (s *Store) readTable(ctx context.Context, name, columns, where, arg string) ([][]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM `+name+` WHERE `+where, arg)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns of %s: %w", name, err)
	}

	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan %s: %w", name, err)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", name, err)
	}
	return out, nil
}
