package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// CreateLibrary inserts a library.
//
// It deliberately does NOT write the five §3 settings columns — slots,
// max_age_days, default_copies, approval_required and steward_contact.
// Migration 2's column defaults supply them, which is what keeps DESIGN.md
// §3's numbers in one place. The consequence matters to callers: setting
// Slots or DefaultCopies on the struct passed here has no effect, and the
// value handed back by LibraryBySlug or LibraryByID is the only one that
// reflects the database. Read it back rather than reusing what you passed.
func (s *Store) CreateLibrary(ctx context.Context, lib boulevard.Library) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO libraries (id, slug, name, location_label, base_url, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		string(lib.ID), lib.Slug, lib.Name, lib.LocationLabel, lib.BaseURL,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create library %q: %w", lib.Slug, err)
	}
	return nil
}

func (s *Store) LibraryBySlug(ctx context.Context, slug string) (boulevard.Library, error) {
	return s.scanLibrary(s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, location_label, base_url,
		        slots, max_age_days, default_copies, approval_required, steward_contact
		   FROM libraries WHERE slug = ?`, slug), slug)
}

// LibrarySlugs lists every library slug in the database, ordered.
//
// One database can hold many libraries (DESIGN.md §10), and the slug is
// derived from --name, so a typo mints a whole new library rather than
// reprinting. The CLI names the neighbours before it does that.
func (s *Store) LibrarySlugs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT slug FROM libraries ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("scan library slug: %w", err)
		}
		out = append(out, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	return out, nil
}

// UpdateLibrary overwrites the mutable fields. The ID is the match key and
// never changes — printed artifacts and copied database files depend on it.
func (s *Store) UpdateLibrary(ctx context.Context, lib boulevard.Library) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET slug = ?, name = ?, location_label = ?, base_url = ?,
		        slots = ?, max_age_days = ?, default_copies = ?,
		        approval_required = ?, steward_contact = ?
		   WHERE id = ?`,
		lib.Slug, lib.Name, lib.LocationLabel, lib.BaseURL,
		lib.Slots, lib.MaxAgeDays, lib.DefaultCopies,
		boolToInt(lib.ApprovalRequired), lib.StewardContact, string(lib.ID))
	if err != nil {
		return fmt.Errorf("update library %q: %w", lib.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update library %q: %w", lib.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update library %q: %w", lib.ID, ErrNotFound)
	}
	return nil
}

// LibraryByID loads a library by its stable identifier.
func (s *Store) LibraryByID(ctx context.Context, id boulevard.LibraryID) (boulevard.Library, error) {
	return s.scanLibrary(s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, location_label, base_url,
		        slots, max_age_days, default_copies, approval_required, steward_contact
		   FROM libraries WHERE id = ?`, string(id)), string(id))
}

// scanLibrary is shared so the column list and the scan list cannot drift
// apart — a transposition between two adjacent TEXT columns compiles fine
// and produces a plausible, wrong library.
func (s *Store) scanLibrary(row *sql.Row, what string) (boulevard.Library, error) {
	var lib boulevard.Library
	var id string
	var approval int
	err := row.Scan(&id, &lib.Slug, &lib.Name, &lib.LocationLabel, &lib.BaseURL,
		&lib.Slots, &lib.MaxAgeDays, &lib.DefaultCopies, &approval, &lib.StewardContact)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Library{}, fmt.Errorf("library %q: %w", what, ErrNotFound)
	}
	if err != nil {
		return boulevard.Library{}, fmt.Errorf("look up library %q: %w", what, err)
	}
	lib.ID = boulevard.LibraryID(id)
	lib.ApprovalRequired = approval == 1
	return lib, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
