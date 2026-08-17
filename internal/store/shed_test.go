package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func TestShedItemsNewestFirst(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	older := shelvedTestItemAt(t, st, lib.ID, "OLDER", now.Add(-2*time.Hour))
	newer := shelvedTestItemAt(t, st, lib.ID, "NEWER", now.Add(-time.Hour))
	shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)

	for i, it := range []boulevard.Item{older, newer} {
		at := now.Add(time.Duration(i) * time.Hour)
		if _, err := st.db.Exec(
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = 'expired'
			  WHERE id = ?`, at.UTC().Format(time.RFC3339), it.ID); err != nil {
			t.Fatalf("shed %s: %v", it.ID, err)
		}
	}

	got, err := st.ShedItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("shed items: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d shed items, want 2", len(got))
	}
	if got[0].ID != newer.ID {
		t.Errorf("first shed item = %s, want %s (newest first)", got[0].ID, newer.ID)
	}
}

func TestReshelveRestoresCopiesAndRestartsTheClock(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now.AddDate(0, 0, -200))
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = 'taken',
		        copies_left = 0 WHERE id = ?`,
		now.UTC().Format(time.RFC3339), it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatalf("reshelve: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
	if got.CopiesLeft != got.CopiesTotal {
		t.Errorf("copies_left = %d, want copies_total %d", got.CopiesLeft, got.CopiesTotal)
	}
	if got.ShedAt != nil || got.ShedReason != boulevard.ShedNone {
		t.Error("shed_at/shed_reason were not cleared")
	}
	// A re-shelved item is new to the shelf again, so its expiry clock
	// restarts. Otherwise it would shed again on the next sweep.
	if got.ShelvedAt == nil || !got.ShelvedAt.Equal(now.UTC().Truncate(time.Second)) {
		t.Errorf("shelved_at = %v, want %v", got.ShelvedAt, now.UTC())
	}
}

func TestReshelveRefusesAFullShelf(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)
	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now.Add(-time.Hour))
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now)
	if !errors.Is(err, ErrShelfFull) {
		t.Fatalf("reshelve error = %v, want ErrShelfFull", err)
	}

	on, err := st.ItemByID(context.Background(), lib.ID, "ONSHELF")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if on.State != boulevard.ItemShelved {
		t.Error("reshelve evicted a live item to make room")
	}
}

func TestReleaseFromTheShed(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	if err := st.ReleaseItem(context.Background(), lib.ID, it.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Soft delete: the row stays, so a steward who released the wrong thing
	// has not destroyed it.
	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %s, want released", got.State)
	}
}

// takeSession creates an additional session against the same library,
// reusing whichever token is on file — sessions.token_id has no uniqueness
// constraint, so the same card scanned twice mints two sessions, the same
// pattern take_test.go uses for its second-session cases.
func takeSession(t *testing.T, st *Store, lib boulevard.Library, tok boulevard.Token, id boulevard.SessionID, now time.Time) boulevard.SessionID {
	t.Helper()
	sess := boulevard.Session{
		ID: id, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
	return sess.ID
}

// TestReshelveClearsStaleTakeRowsSoUndoIsANoOp is the F1 sequence: three
// sessions take an item to zero, the steward re-shelves it, a fourth
// session takes a copy, and one of the original takers taps undo. Before
// this fix the stale session_takes row survived ReshelveItem and armed
// UntakeItem's un-shed branch, so the stranger's stale undo could restore a
// copy the steward already restored and decrement takes for a take that no
// longer exists. It must now be a true no-op.
func TestReshelveClearsStaleTakeRowsSoUndoIsANoOp(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	ctx := context.Background()

	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now) // copies_total = 3

	s := takeSession(t, st, lib, toks[0], "S", now)
	tt := takeSession(t, st, lib, toks[0], "T", now)
	u := takeSession(t, st, lib, toks[0], "U", now)
	v := takeSession(t, st, lib, toks[0], "V", now)

	for _, sess := range []boulevard.SessionID{s, tt, u} {
		if err := st.TakeItem(ctx, lib.ID, sess, it.ID, now, 3); err != nil {
			t.Fatalf("take by %s: %v", sess, err)
		}
	}
	got, err := st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed || got.CopiesLeft != 0 || got.Takes != 3 {
		t.Fatalf("after three takes: state=%s copies_left=%d takes=%d, want shed/0/3",
			got.State, got.CopiesLeft, got.Takes)
	}

	if err := st.ReshelveItem(ctx, lib.ID, it.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("reshelve: %v", err)
	}

	if err := st.TakeItem(ctx, lib.ID, v, it.ID, now.Add(2*time.Minute), 3); err != nil {
		t.Fatalf("take by V: %v", err)
	}
	got, err = st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 || got.Takes != 4 {
		t.Fatalf("after V's take: copies_left=%d takes=%d, want 2/4", got.CopiesLeft, got.Takes)
	}

	// S taps undo on a take the steward already resolved. It must be a
	// silent no-op: no row to delete, nothing to restore, nothing to
	// decrement.
	if err := st.UntakeItem(ctx, lib.ID, s, it.ID); err != nil {
		t.Fatalf("stale undo by S: %v", err)
	}
	got, err = st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 {
		t.Errorf("copies_left = %d, want 2 — a stale undo restored a copy the steward already restored", got.CopiesLeft)
	}
	if got.Takes != 4 {
		t.Errorf("takes = %d, want 4 — a stale undo decremented a take that no longer exists", got.Takes)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}

	// T and U's stale rows must be gone too, not just S's.
	for _, sess := range []boulevard.SessionID{tt, u} {
		if err := st.UntakeItem(ctx, lib.ID, sess, it.ID); err != nil {
			t.Fatalf("stale undo by %s: %v", sess, err)
		}
	}
	got, err = st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 || got.Takes != 4 {
		t.Errorf("after T and U's stale undos: copies_left=%d takes=%d, want unchanged 2/4",
			got.CopiesLeft, got.Takes)
	}
}

// TestReshelveClearsTakenBySession is the second F1 test: after a
// re-shelve, TakenBySession must report nothing for a session that had
// taken the item, because the steward's decision — not the session's
// outstanding take — is what the shelf reflects now.
func TestReshelveClearsTakenBySession(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	ctx := context.Background()

	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	s := takeSession(t, st, lib, toks[0], "S", now)
	if err := st.TakeItem(ctx, lib.ID, s, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	taken, err := st.TakenBySession(ctx, s)
	if err != nil {
		t.Fatalf("taken before reshelve: %v", err)
	}
	if !taken[it.ID] {
		t.Fatal("setup: expected the take row to exist before reshelve")
	}

	if err := st.ReshelveItem(ctx, lib.ID, it.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("reshelve: %v", err)
	}

	taken, err = st.TakenBySession(ctx, s)
	if err != nil {
		t.Fatalf("taken after reshelve: %v", err)
	}
	if taken[it.ID] {
		t.Error("TakenBySession still reports the item after reshelve — the stale take row survived")
	}
}

// TestReshelveThenStaleUndoDoesNotUnshedAnItemEmptiedByAnotherSession is the
// third F1 test: after a re-shelve, a different session legitimately takes
// the item to zero. The original taker's stale undo — its session_takes row
// gone, deleted by the reshelve — must not un-shed an item somebody else
// emptied.
func TestReshelveThenStaleUndoDoesNotUnshedAnItemEmptiedByAnotherSession(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	ctx := context.Background()

	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	a := takeSession(t, st, lib, toks[0], "A", now)
	if err := st.TakeItem(ctx, lib.ID, a, it.ID, now, 3); err != nil {
		t.Fatalf("A takes: %v", err)
	}

	if err := st.ReshelveItem(ctx, lib.ID, it.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("reshelve: %v", err)
	}

	b := takeSession(t, st, lib, toks[0], "B", now.Add(2*time.Minute))
	if err := st.TakeItem(ctx, lib.ID, b, it.ID, now.Add(2*time.Minute), 3); err != nil {
		t.Fatalf("B takes to zero: %v", err)
	}
	got, err := st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed || got.ShedReason != boulevard.ShedTaken || got.CopiesLeft != 0 {
		t.Fatalf("after B's take: state=%s reason=%q copies_left=%d, want shed/taken/0",
			got.State, got.ShedReason, got.CopiesLeft)
	}

	// A's stale undo: A's row was deleted by the reshelve, so this must be a
	// no-op and must not touch the item B legitimately emptied.
	if err := st.UntakeItem(ctx, lib.ID, a, it.ID); err != nil {
		t.Fatalf("A's stale undo: %v", err)
	}
	got, err = st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — a stale undo un-shed an item another session emptied", got.State)
	}
	if got.ShedReason != boulevard.ShedTaken {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedTaken)
	}
	if got.CopiesLeft != 0 {
		t.Errorf("copies_left = %d, want 0 — a stale undo restored a copy that belongs to B's live take", got.CopiesLeft)
	}
}

func TestReshelveAndReleaseRefuseItemsNotInTheShed(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	it := shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)

	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now); !errors.Is(err, ErrNotShed) {
		t.Errorf("reshelve error = %v, want ErrNotShed", err)
	}
	if err := st.ReleaseItem(context.Background(), lib.ID, it.ID); !errors.Is(err, ErrNotShed) {
		t.Errorf("release error = %v, want ErrNotShed", err)
	}
}
