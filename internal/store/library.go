package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

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
	var lib boulevard.Library
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, location_label, base_url FROM libraries WHERE slug = ?`, slug).
		Scan(&id, &lib.Slug, &lib.Name, &lib.LocationLabel, &lib.BaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Library{}, fmt.Errorf("library %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return boulevard.Library{}, fmt.Errorf("look up library %q: %w", slug, err)
	}
	lib.ID = boulevard.LibraryID(id)
	return lib, nil
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
		`UPDATE libraries SET slug = ?, name = ?, location_label = ?, base_url = ? WHERE id = ?`,
		lib.Slug, lib.Name, lib.LocationLabel, lib.BaseURL, string(lib.ID))
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
	var lib boulevard.Library
	var got string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, location_label, base_url FROM libraries WHERE id = ?`, string(id)).
		Scan(&got, &lib.Slug, &lib.Name, &lib.LocationLabel, &lib.BaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Library{}, fmt.Errorf("library %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return boulevard.Library{}, fmt.Errorf("look up library %q: %w", id, err)
	}
	lib.ID = boulevard.LibraryID(got)
	return lib, nil
}
