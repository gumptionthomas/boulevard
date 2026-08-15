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

func TestReshelveAndReleaseRefuseItemsNotInTheShed(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	it := shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)

	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now); err == nil {
		t.Error("reshelved an item that was already on the shelf")
	}
	if err := st.ReleaseItem(context.Background(), lib.ID, it.ID); err == nil {
		t.Error("released an item from the shelf rather than the shed")
	}
}
