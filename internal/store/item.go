package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

const itemColumns = `id, library_id, type, payload, note, attribution,
                     copies_total, copies_left, state, pinned, views, takes,
                     left_at, shelved_at`

func (s *Store) CreateItem(ctx context.Context, id boulevard.LibraryID, it boulevard.Item) error {
	if it.LibraryID != id {
		return fmt.Errorf("item %s belongs to library %q, not %q", it.ID, it.LibraryID, id)
	}
	var shelved any
	if it.ShelvedAt != nil {
		shelved = it.ShelvedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO items (`+itemColumns+`, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(id), string(it.Type), it.Payload, it.Note, it.Attribution,
		it.CopiesTotal, it.CopiesLeft, string(it.State), boolToInt(it.Pinned),
		it.Views, it.Takes,
		it.LeftAt.UTC().Format(time.RFC3339), shelved,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create item %s: %w", it.ID, err)
	}
	return nil
}

// ShelvedItems is the public shelf: most recently shelved first, ties broken
// by id so the order is total and stable across requests.
func (s *Store) ShelvedItems(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Item, error) {
	return s.itemsWhere(ctx, id,
		`WHERE library_id = ? AND state = 'shelved' ORDER BY shelved_at DESC, id DESC`)
}

// PendingItems is the approval queue: oldest first, because whoever left
// something first should be looked at first.
func (s *Store) PendingItems(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Item, error) {
	return s.itemsWhere(ctx, id,
		`WHERE library_id = ? AND state = 'pending' ORDER BY left_at ASC, id ASC`)
}

func (s *Store) itemsWhere(ctx context.Context, id boulevard.LibraryID, clause string) ([]boulevard.Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items `+clause, string(id))
	if err != nil {
		return nil, fmt.Errorf("list items for %q: %w", id, err)
	}
	defer rows.Close()

	var out []boulevard.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemByID reads one item, scoped to its library. An item belonging to
// another library reports ErrNotFound rather than being readable across the
// boundary.
func (s *Store) ItemByID(ctx context.Context, id boulevard.LibraryID, itemID string) (boulevard.Item, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE library_id = ? AND id = ?`, string(id), itemID)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Item{}, fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	return it, err
}

func (s *Store) IncrementViews(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE items SET views = views + 1 WHERE library_id = ? AND id = ?`,
		string(id), itemID)
	if err != nil {
		return fmt.Errorf("increment views on %s: %w", itemID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("increment views on %s: %w", itemID, err)
	}
	if n == 0 {
		return fmt.Errorf("increment views on %s: %w", itemID, ErrNotFound)
	}
	return nil
}

// ApproveItem shelves a pending item, evicting the oldest if the shelf is
// already at capacity, and reports which item it shed.
//
// Both writes happen in one transaction. A shed item with no replacement
// would silently shrink the shelf, and a shelved item that failed to evict
// would grow it past its slots — a shelf that is not finite is not the
// object DESIGN.md describes.
//
// §5's rule is "the oldest non-pinned item". Nothing is pinned in this
// milestone, so it reduces to the oldest by shelved_at. FIFO is deliberately
// dumb and deliberately not attention-weighted: letting popular items
// survive longer would rebuild the ranking this project reacts against.
func (s *Store) ApproveItem(ctx context.Context, lib boulevard.Library, itemID string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin approve: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(lib.ID), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemPending {
		return "", fmt.Errorf("item %q is %s, not pending", itemID, state)
	}

	var shelved int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved'`,
		string(lib.ID)).Scan(&shelved); err != nil {
		return "", fmt.Errorf("count the shelf: %w", err)
	}

	var evicted string
	if shelved >= lib.Slots {
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM items
			  WHERE library_id = ? AND state = 'shelved' AND pinned = 0
			  ORDER BY shelved_at ASC, id ASC LIMIT 1`,
			string(lib.ID)).Scan(&evicted); err != nil {
			return "", fmt.Errorf("find the oldest shelved item: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shed' WHERE library_id = ? AND id = ?`,
			string(lib.ID), evicted); err != nil {
			return "", fmt.Errorf("shed item %q: %w", evicted, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items
		    SET state = 'shelved', shelved_at = ?, copies_total = ?, copies_left = ?
		  WHERE library_id = ? AND id = ?`,
		now.UTC().Format(time.RFC3339), lib.DefaultCopies, lib.DefaultCopies,
		string(lib.ID), itemID); err != nil {
		return "", fmt.Errorf("shelve item %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit approve: %w", err)
	}
	return evicted, nil
}

// RejectItem releases a pending item. §5 calls release a soft delete: the
// row stays, so a steward who rejects the wrong thing has not destroyed it.
func (s *Store) RejectItem(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE items SET state = 'released'
		  WHERE library_id = ? AND id = ? AND state = 'pending'`,
		string(id), itemID)
	if err != nil {
		return fmt.Errorf("reject item %q: %w", itemID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reject item %q: %w", itemID, err)
	}
	if n == 0 {
		return fmt.Errorf("item %q is not pending: %w", itemID, ErrNotFound)
	}
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves the single-item and list paths and they cannot drift apart.
type scanner interface{ Scan(dest ...any) error }

func scanItem(sc scanner) (boulevard.Item, error) {
	var (
		it      boulevard.Item
		libID   string
		typ     string
		state   string
		pinned  int
		left    string
		shelved *string
	)
	if err := sc.Scan(&it.ID, &libID, &typ, &it.Payload, &it.Note, &it.Attribution,
		&it.CopiesTotal, &it.CopiesLeft, &state, &pinned, &it.Views, &it.Takes,
		&left, &shelved); err != nil {
		return boulevard.Item{}, err
	}
	it.LibraryID = boulevard.LibraryID(libID)
	it.Type = boulevard.ItemType(typ)
	it.State = boulevard.ItemState(state)
	it.Pinned = pinned == 1

	var err error
	if it.LeftAt, err = time.Parse(time.RFC3339, left); err != nil {
		return boulevard.Item{}, fmt.Errorf("item %s left_at: %w", it.ID, err)
	}
	if shelved != nil {
		at, err := time.Parse(time.RFC3339, *shelved)
		if err != nil {
			return boulevard.Item{}, fmt.Errorf("item %s shelved_at: %w", it.ID, err)
		}
		it.ShelvedAt = &at
	}
	return it, nil
}
