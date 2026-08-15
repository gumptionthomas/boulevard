package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// ShedItems is the steward's shed: most recently shed first, because the
// decision a steward is making is usually about what just left.
//
// Evicted items are not public and not deleted. The shelf sheds them —
// passively, the way a tree sheds leaves, with no judgment implied (§5).
func (s *Store) ShedItems(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Item, error) {
	return s.itemsWhere(ctx, id,
		`WHERE library_id = ? AND state = 'shed' ORDER BY shed_at DESC, id DESC`)
}

// ReshelveItem returns a shed item to the shelf.
//
// shelved_at is set to now, restarting the expiry clock: a re-shelved item
// is new to the shelf again, and keeping the old timestamp would shed it
// again on the very next sweep. Copies are restored to copies_total, since
// an item taken to zero would otherwise return with nothing to take.
//
// It refuses a full shelf rather than evicting, for the reason UntakeItem
// does: the steward asked to add one item, not to remove another. §5 also
// warns against mechanics that generate steward labor — silently evicting
// here would create exactly the surprise that produces more of it.
func (s *Store) ReshelveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reshelve: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemShed {
		return fmt.Errorf("item %q is %s, not in the shed: %w", itemID, state, ErrNotShed)
	}

	var slots, shelved int
	if err := tx.QueryRowContext(ctx,
		`SELECT slots FROM libraries WHERE id = ?`, string(id)).Scan(&slots); err != nil {
		return fmt.Errorf("read slots for %q: %w", id, err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved'`,
		string(id)).Scan(&shelved); err != nil {
		return fmt.Errorf("count the shelf: %w", err)
	}
	if shelved >= slots {
		return fmt.Errorf("shelf has %d of %d slots: %w", shelved, slots, ErrShelfFull)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items
		    SET state = 'shelved', shelved_at = ?, shed_at = NULL, shed_reason = '',
		        copies_left = copies_total
		  WHERE library_id = ? AND id = ?`,
		now.UTC().Format(time.RFC3339), string(id), itemID); err != nil {
		return fmt.Errorf("reshelve %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reshelve: %w", err)
	}
	return nil
}

// ReleaseItem soft-deletes a shed item. The row stays, so a steward who
// releases the wrong thing has not destroyed it — the same soft delete
// RejectItem applies to the approval queue.
func (s *Store) ReleaseItem(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin release: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemShed {
		return fmt.Errorf("item %q is %s, not in the shed: %w", itemID, state, ErrNotShed)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET state = 'released' WHERE library_id = ? AND id = ?`,
		string(id), itemID); err != nil {
		return fmt.Errorf("release %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit release: %w", err)
	}
	return nil
}
