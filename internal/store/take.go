package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// TakeItem records that a session took one copy of an item (DESIGN.md §5).
//
// Taking is a write. It decrements a finite shelf, so it is gated exactly
// like leaving — take feels passive because you are receiving, but on a
// finite shelf removal is as much an edit as addition.
//
// Everything happens in one transaction: the take row, the decrement, and
// the shed at zero. Two of the three without the others would leave the
// shelf describing something that did not happen.
//
// A duplicate take by the same session on the same item is a no-op that
// returns nil. Phones retry, and a retry must not spend a second copy or
// produce a failure page.
func (s *Store) TakeItem(ctx context.Context, id boulevard.LibraryID,
	sessionID boulevard.SessionID, itemID string, now time.Time, maxTakes int) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin take: %w", err)
	}
	defer tx.Rollback()

	// Already taken by this session? Nothing to do.
	var already int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_takes WHERE session_id = ? AND item_id = ?`,
		string(sessionID), itemID).Scan(&already); err != nil {
		return fmt.Errorf("check existing take: %w", err)
	}
	if already > 0 {
		return nil
	}

	var takes int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_takes WHERE session_id = ?`,
		string(sessionID)).Scan(&takes); err != nil {
		return fmt.Errorf("count takes: %w", err)
	}
	if takes >= maxTakes {
		return fmt.Errorf("session has taken %d: %w", takes, ErrLimitReached)
	}

	var state string
	var copiesLeft, pinned int
	err = tx.QueryRowContext(ctx,
		`SELECT state, copies_left, pinned FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state, &copiesLeft, &pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	// A pinned item has no take control to press, so a take against one is
	// not a refusal to explain — it is a URL that names nothing takeable.
	if boulevard.ItemState(state) != boulevard.ItemShelved || pinned == 1 {
		return fmt.Errorf("item %q is not takeable: %w", itemID, ErrNotFound)
	}
	if copiesLeft <= 0 {
		return fmt.Errorf("item %q: %w", itemID, ErrNoCopiesLeft)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_takes (session_id, item_id, taken_at) VALUES (?, ?, ?)`,
		string(sessionID), itemID, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record take: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET copies_left = copies_left - 1, takes = takes + 1
		  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
		return fmt.Errorf("decrement copies on %q: %w", itemID, err)
	}

	if copiesLeft-1 == 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = ?
			  WHERE library_id = ? AND id = ?`,
			now.UTC().Format(time.RFC3339), string(boulevard.ShedTaken),
			string(id), itemID); err != nil {
			return fmt.Errorf("shed item %q at zero: %w", itemID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit take: %w", err)
	}
	return nil
}

// UntakeItem reverses a take while the session that made it still exists
// (spec §4). This is the whole reason sessions became sweepable: the take
// row is deleted with the session, so the undo window closes on its own.
//
// It checks shed_reason, not just state. An item shed by expiry or eviction
// while the take was outstanding must not be silently re-shelved by a
// passer-by's undo — that is the steward's call. The same guard covers an
// item the steward released out of the shed: `released` is not `shed`, so
// the un-shed does not fire, and a steward's release cannot be reversed by
// a stranger.
//
// A duplicate undo is a no-op that returns nil, for the retry reason
// TakeItem documents.
//
// ErrShelfFull here means "committed, but not re-shelved" — not "nothing
// happened". §4's full-shelf case refuses only the *re-shelve*: the take row
// is still deleted and the copy still restored, so the take stops counting
// against the session's limit, while the item itself stays in the shed for
// the steward. Only the re-shelve is conditional on there being room; the
// delete and the restore are not, which is why they run and commit before
// the capacity check ever has a say. The capacity check also has to run
// after the delete for a second reason: it must not fire for a session that
// never took this item in the first place (RowsAffected == 0), which the
// duplicate-undo no-op rule requires regardless of shelf capacity.
//
// The copy restore is clamped to copies_total. A steward can re-shelve a
// taken-to-zero item (ReshelveItem sets copies_left = copies_total) inside
// the same 24-hour window the original taker's undo is still valid in; an
// unclamped +1 on top of that re-shelve would hand out a copy that never
// existed.
func (s *Store) UntakeItem(ctx context.Context, id boulevard.LibraryID,
	sessionID boulevard.SessionID, itemID string) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin untake: %w", err)
	}
	defer tx.Rollback()

	var state, reason string
	err = tx.QueryRowContext(ctx,
		`SELECT state, shed_reason FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}

	unshed := boulevard.ItemState(state) == boulevard.ItemShed &&
		boulevard.ShedReason(reason) == boulevard.ShedTaken

	res, err := tx.ExecContext(ctx,
		`DELETE FROM session_takes WHERE session_id = ? AND item_id = ?`,
		string(sessionID), itemID)
	if err != nil {
		return fmt.Errorf("delete take: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete take: %w", err)
	}
	if n == 0 {
		return nil // nothing was taken; nothing to undo
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items
		    SET copies_left = MIN(copies_left + 1, copies_total), takes = takes - 1
		  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
		return fmt.Errorf("restore copy on %q: %w", itemID, err)
	}

	// Capacity is checked only now that the delete has proven this session
	// actually had something to undo, and only when the undo would actually
	// put an item back. Refusing to re-shelve is deliberate: making room
	// would evict someone else's item to fix this person's stray tap. The
	// take row and the copy are not held hostage to that refusal — see the
	// doc comment above.
	full := false
	if unshed {
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
			full = true
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE items SET state = 'shelved', shed_at = NULL, shed_reason = ''
				  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
				return fmt.Errorf("re-shelve %q: %w", itemID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit untake: %w", err)
	}
	if full {
		return fmt.Errorf("undid the take, but the shelf is full: %w", ErrShelfFull)
	}
	return nil
}

// TakenBySession is the set of item ids this session has taken, for
// rendering the take control's "Taken / Put it back" state.
//
// A set rather than a count: the shelf needs to know which items, and the
// rate limit needs how many, and len() answers the second from the first.
// One query, no second number to keep in sync.
func (s *Store) TakenBySession(ctx context.Context, sessionID boulevard.SessionID) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id FROM session_takes WHERE session_id = ?`, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list takes for session: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan take: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}
