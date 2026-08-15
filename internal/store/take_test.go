package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func takeSetup(t *testing.T) (*Store, boulevard.Library, boulevard.SessionID, time.Time) {
	t.Helper()
	loc := chicago(t)
	st := openTemp(t)
	// seededLibrary, not seedLibrary: sessions.token_id is a real
	// REFERENCES tokens(id) foreign key and the DSN sets foreign_keys(1),
	// so a session carrying a made-up token id fails to insert at all.
	// seededLibrary inserts a real twelve-card booklet and hands it back.
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	sess := boulevard.Session{
		ID: "SESSION1", LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return st, lib, sess.ID, now
}

func TestTakeItemDecrementsAndCounts(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 {
		t.Errorf("copies_left = %d, want 2", got.CopiesLeft)
	}
	if got.Takes != 1 {
		t.Errorf("takes = %d, want 1", got.Takes)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
}

func TestTakeToZeroSheds(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Fatalf("state = %s, want shed", got.State)
	}
	if got.ShedReason != boulevard.ShedTaken {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedTaken)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set")
	}
}

func TestDuplicateTakeIsANoOpNotAnError(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("first take: %v", err)
	}
	// Phones retry. A duplicate must not spend a second copy or fail.
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("second take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 {
		t.Errorf("copies_left = %d, want 2 — the retry spent a copy", got.CopiesLeft)
	}
	if got.Takes != 1 {
		t.Errorf("takes = %d, want 1", got.Takes)
	}
}

func TestTakeRefusesPastTheLimit(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	for i, id := range []string{"I1", "I2", "I3"} {
		it := shelvedTestItemAt(t, st, lib.ID, id, now.Add(time.Duration(i)*time.Minute))
		if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
			t.Fatalf("take %s: %v", id, err)
		}
	}
	fourth := shelvedTestItemAt(t, st, lib.ID, "I4", now)

	err := st.TakeItem(context.Background(), lib.ID, sess, fourth.ID, now, 3)
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("fourth take error = %v, want ErrLimitReached", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, fourth.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — a refused take spent a copy", got.CopiesLeft)
	}
}

func TestTakeRefusesPinnedItems(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "PINNED", now)
	if _, err := st.db.Exec(`UPDATE items SET pinned = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("pin: %v", err)
	}

	err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("take of a pinned item = %v, want ErrNotFound — pins cannot be taken (§5)", err)
	}
}

func TestTakeRefusesWhenNoCopiesLeft(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "EMPTY", now)
	// The default_copies = 0 case: shelved with nothing to take.
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 0, copies_total = 0 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("empty it: %v", err)
	}

	err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3)
	if !errors.Is(err, ErrNoCopiesLeft) {
		t.Fatalf("error = %v, want ErrNoCopiesLeft", err)
	}
}

func TestUntakeRestoresAndUnsheds(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
	if got.CopiesLeft != 1 {
		t.Errorf("copies_left = %d, want 1", got.CopiesLeft)
	}
	if got.Takes != 0 {
		t.Errorf("takes = %d, want 0", got.Takes)
	}
	if got.ShedAt != nil || got.ShedReason != boulevard.ShedNone {
		t.Errorf("shed_at/%v shed_reason/%q were not cleared", got.ShedAt, got.ShedReason)
	}

	taken, err := st.TakenBySession(context.Background(), sess)
	if err != nil {
		t.Fatalf("taken: %v", err)
	}
	if taken[it.ID] {
		t.Error("the take row survived the undo")
	}
}

func TestUntakeDoesNotUnshedAnItemShedForAnotherReason(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// It expires while the take is outstanding. The steward decides whether
	// that comes back, not a passer-by.
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — undo un-shed an expired item", got.State)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — the copy should still return", got.CopiesLeft)
	}
}

func TestUntakeRefusesAFullShelf(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	it := shelvedTestItemAt(t, st, lib.ID, "TAKEN", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// Someone else's item fills the slot the take freed.
	shelvedTestItemAt(t, st, lib.ID, "NEWER", now.Add(time.Minute))

	err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID)
	if !errors.Is(err, ErrShelfFull) {
		t.Fatalf("untake error = %v, want ErrShelfFull", err)
	}

	// ErrShelfFull means "committed, but not re-shelved" — the spec (§4)
	// refuses only the re-shelve. The take row is still deleted and the
	// copy still restored, so the take stops counting against the
	// session's limit even though the item stays in the shed.
	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 1 {
		t.Errorf("copies_left = %d, want 1 — the copy should still return", got.CopiesLeft)
	}
	if got.Takes != 0 {
		t.Errorf("takes = %d, want 0", got.Takes)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — the item stays in the shed for the steward", got.State)
	}
	if got.ShedReason != boulevard.ShedTaken {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedTaken)
	}

	taken, err := st.TakenBySession(context.Background(), sess)
	if err != nil {
		t.Fatalf("taken: %v", err)
	}
	if taken[it.ID] {
		t.Error("the take row survived the refusal — undo can never succeed on this item again")
	}

	newer, err := st.ItemByID(context.Background(), lib.ID, "NEWER")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if newer.State != boulevard.ItemShelved {
		t.Error("undo evicted a different item to make room")
	}
}

// TestUntakeByANonTakerOnAFullShelfIsANoOp is the capacity check's
// ordering: a session with no session_takes row for this item must get the
// duplicate-undo nil, not ErrShelfFull, even when the item is shed-taken
// and the shelf is full. RowsAffected is the authority on "did this session
// take it", and the capacity check must not run ahead of it.
func TestUntakeByANonTakerOnAFullShelfIsANoOp(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	it := shelvedTestItemAt(t, st, lib.ID, "TAKEN", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// Someone else's item fills the slot the take freed.
	shelvedTestItemAt(t, st, lib.ID, "NEWER", now.Add(time.Minute))

	// A second session, with no take row on this item at all. token_id has
	// no uniqueness constraint on sessions — the same card scanned twice
	// mints two sessions — so reusing whichever token is on file is fine.
	toks, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil || len(toks) == 0 {
		t.Fatalf("tokens for library: %v", err)
	}
	other := boulevard.Session{
		ID: "SESSION2", LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), other); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := st.UntakeItem(context.Background(), lib.ID, other.ID, it.ID); err != nil {
		t.Fatalf("untake by a non-taker = %v, want nil", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed || got.ShedReason != boulevard.ShedTaken {
		t.Errorf("state/%s reason/%q changed — a non-taker's undo touched the item", got.State, got.ShedReason)
	}
	// The one copy was already spent to zero by sess's own take; a
	// non-taker's undo must not restore it.
	if got.CopiesLeft != 0 {
		t.Errorf("copies_left = %d, want 0 unchanged — a non-taker's undo restored a copy", got.CopiesLeft)
	}
}

// TestUntakeClampsRestoreToCopiesTotal covers the take → steward re-shelve
// → undo collision: ReshelveItem sets copies_left = copies_total, and an
// unclamped +1 on top of that hands out a copy that never existed.
func TestUntakeClampsRestoreToCopiesTotal(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// The steward re-shelves inside the 24-hour undo window.
	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("reshelve: %v", err)
	}
	// The original taker, unaware, presses undo.
	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != got.CopiesTotal {
		t.Errorf("copies_left = %d, copies_total = %d — undo handed out a copy that never existed",
			got.CopiesLeft, got.CopiesTotal)
	}
	if got.CopiesLeft != 1 {
		t.Errorf("copies_left = %d, want 1", got.CopiesLeft)
	}
}

func TestDuplicateUntakeIsANoOp(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("first untake: %v", err)
	}
	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("second untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — the retry restored a phantom copy", got.CopiesLeft)
	}
}
