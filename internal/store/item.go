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
                     left_at, shelved_at, shed_at, shed_reason`

func (s *Store) CreateItem(ctx context.Context, id boulevard.LibraryID, it boulevard.Item) error {
	if it.LibraryID != id {
		return fmt.Errorf("item %s belongs to library %q, not %q", it.ID, it.LibraryID, id)
	}
	var shelved any
	if it.ShelvedAt != nil {
		shelved = it.ShelvedAt.UTC().Format(time.RFC3339)
	}
	var shedAt any
	if it.ShedAt != nil {
		shedAt = it.ShedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO items (`+itemColumns+`, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(id), string(it.Type), it.Payload, it.Note, it.Attribution,
		it.CopiesTotal, it.CopiesLeft, string(it.State), boolToInt(it.Pinned),
		it.Views, it.Takes,
		it.LeftAt.UTC().Format(time.RFC3339), shelved,
		shedAt, string(it.ShedReason),
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
// It takes a LibraryID and reads `slots` and `default_copies` here, inside
// that transaction, rather than taking a caller-supplied Library and
// trusting its fields. CreateLibrary does not write the settings columns
// (migration defaults do), so a Library value that never came back from the
// database carries Slots: 0 — which makes the capacity test below
// unconditionally true and sheds a live item on an empty shelf. Capacity
// belongs under the same lock as the count it is compared against.
//
// §5's rule is "the oldest non-pinned item". Nothing is pinned in this
// milestone, so it reduces to the oldest by shelved_at. FIFO is deliberately
// dumb and deliberately not attention-weighted: letting popular items
// survive longer would rebuild the ranking this project reacts against.
//
// `approve` runs in a separate process from `serve`, so the two can hold
// this transaction at once. It opens deferred and upgrades to a write lock
// at the first UPDATE. That upgrade can fail two different ways, and only
// one of them is what busy_timeout absorbs: an ordinary write-lock wait
// (another writer mid-transaction right now) retries and usually succeeds
// within the timeout, but in WAL mode a deferred transaction can instead hit
// SQLITE_BUSY_SNAPSHOT — its read snapshot is stale because some other
// writer committed since this transaction opened — and busy_timeout does
// not retry that at all; it surfaces immediately. Either failure aborts the
// transaction wholesale via the deferred Rollback above; neither leaves a
// half-applied approval, which is the guarantee that actually holds here,
// not "busy_timeout absorbs it." The read-path sweeps this milestone added
// (SweepExpiredSessions, SweepExpiredItems) run inside their own
// transactions on the same request paths as a concurrent `approve` or
// `reshelve`, which widens the window in which one of them commits between
// this transaction's read and its write and costs it the snapshot. Fixing
// that is `_txlock=immediate` on the DSN, which forces every transaction to
// take its write lock up front instead of deferring — out of scope for this
// fix wave, since it changes the locking mode of every transaction in the
// binary.
func (s *Store) ApproveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin approve: %w", err)
	}
	defer tx.Rollback()

	var slots, defaultCopies int
	err = tx.QueryRowContext(ctx,
		`SELECT slots, default_copies FROM libraries WHERE id = ?`, string(id)).
		Scan(&slots, &defaultCopies)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("library %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read the shelf settings for %q: %w", id, err)
	}

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemPending {
		return "", fmt.Errorf("item %q is %s, not pending: %w", itemID, state, ErrNotPending)
	}

	var shelved int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved'`,
		string(id)).Scan(&shelved); err != nil {
		return "", fmt.Errorf("count the shelf: %w", err)
	}

	var evicted string
	if shelved >= slots {
		// `pinned = 0` can match nothing: Milestone 4 allows 3 pins, so a
		// steward with slots = 3 and three pinned items has a full shelf with
		// nothing evictable. Today no code sets pinned, so this cannot fire;
		// when it can, the answer is to refuse the approval with a message
		// naming the pins, not to evict one.
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM items
			  WHERE library_id = ? AND state = 'shelved' AND pinned = 0
			  ORDER BY shelved_at ASC, id ASC LIMIT 1`,
			string(id)).Scan(&evicted); err != nil {
			return "", fmt.Errorf("find the oldest shelved item: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = ?
			  WHERE library_id = ? AND id = ?`,
			now.UTC().Format(time.RFC3339), string(boulevard.ShedEvicted),
			string(id), evicted); err != nil {
			return "", fmt.Errorf("shed item %q: %w", evicted, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items
		    SET state = 'shelved', shelved_at = ?, copies_total = ?, copies_left = ?
		  WHERE library_id = ? AND id = ?`,
		now.UTC().Format(time.RFC3339), defaultCopies, defaultCopies,
		string(id), itemID); err != nil {
		return "", fmt.Errorf("shelve item %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit approve: %w", err)
	}
	return evicted, nil
}

// RejectItem releases a pending item. §5 calls release a soft delete: the
// row stays, so a steward who rejects the wrong thing has not destroyed it.
//
// It reads the state first, as ApproveItem does, so that "never existed" and
// "already handled" are different answers. Inferring them from
// RowsAffected == 0 could only report one error for both, and the caller
// needs to tell a mistyped id from a second `reject` on the same item.
func (s *Store) RejectItem(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reject: %w", err)
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
	if boulevard.ItemState(state) != boulevard.ItemPending {
		return fmt.Errorf("item %q is %s, not pending: %w", itemID, state, ErrNotPending)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET state = 'released' WHERE library_id = ? AND id = ?`,
		string(id), itemID); err != nil {
		return fmt.Errorf("release item %q: %w", itemID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reject: %w", err)
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
		shedAt  *string
		reason  string
	)
	if err := sc.Scan(&it.ID, &libID, &typ, &it.Payload, &it.Note, &it.Attribution,
		&it.CopiesTotal, &it.CopiesLeft, &state, &pinned, &it.Views, &it.Takes,
		&left, &shelved, &shedAt, &reason); err != nil {
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
	if shedAt != nil {
		at, err := time.Parse(time.RFC3339, *shedAt)
		if err != nil {
			return boulevard.Item{}, fmt.Errorf("item %s shed_at: %w", it.ID, err)
		}
		it.ShedAt = &at
	}
	it.ShedReason = boulevard.ShedReason(reason)
	return it, nil
}
