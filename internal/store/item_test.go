package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// smallShelf creates a library and narrows its shelf to `slots`.
//
// The narrowing is a direct UPDATE of that one column because CreateLibrary
// does not write the settings columns at all — setting Slots on the struct
// it is given has no effect, and going through UpdateLibrary would mean
// reading the whole row back first only to write it out again.
//
// The value returned therefore carries Slots: 0, which is exactly the shape
// that used to be dangerous: ApproveItem reads capacity from the database
// now, so a Library struct that never came back from it cannot mis-evict.
func smallShelf(t *testing.T, s *Store, slots int) boulevard.Library {
	t.Helper()
	lib := makeLibrary(t)
	if err := s.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE libraries SET slots = ? WHERE id = ?`, slots, string(lib.ID)); err != nil {
		t.Fatal(err)
	}
	return lib
}

func makeItem(t *testing.T, lib boulevard.Library, note string, left time.Time) boulevard.Item {
	t.Helper()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	return boulevard.Item{
		ID: id, LibraryID: lib.ID,
		Type: boulevard.ItemLink, Payload: "https://example.org/" + id,
		Note: note, State: boulevard.ItemPending, LeftAt: left,
	}
}

func TestItemRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	want := makeItem(t, lib, "a reason", time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC))
	want.Attribution = "Ruth"

	if err := s.CreateItem(ctx, lib.ID, want); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	got, err := s.ItemByID(ctx, lib.ID, want.ID)
	if err != nil {
		t.Fatalf("ItemByID: %v", err)
	}
	if got.Note != want.Note || got.Payload != want.Payload || got.Attribution != want.Attribution {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.Type != boulevard.ItemLink || got.State != boulevard.ItemPending {
		t.Errorf("type/state = %q/%q", got.Type, got.State)
	}
	if !got.LeftAt.Equal(want.LeftAt) {
		t.Errorf("LeftAt = %v, want %v", got.LeftAt, want.LeftAt)
	}
	if got.ShelvedAt != nil {
		t.Errorf("ShelvedAt = %v, want nil for a pending item", got.ShelvedAt)
	}
}

func TestPendingItemsNeverAppearOnTheShelf(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateItem(ctx, lib.ID, makeItem(t, lib, "waiting", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	shelved, err := s.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelved) != 0 {
		t.Errorf("shelf has %d items, want 0 — a pending item is not public", len(shelved))
	}
	pending, err := s.PendingItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("queue has %d items, want 1", len(pending))
	}
}

func TestShelvedItemsAreNewestFirst(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	for i, note := range []string{"oldest", "middle", "newest"} {
		it := makeItem(t, lib, note, base)
		it.State = boulevard.ItemShelved
		at := base.Add(time.Duration(i) * time.Hour)
		it.ShelvedAt = &at
		if err := s.CreateItem(ctx, lib.ID, it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"newest", "middle", "oldest"}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Note != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i].Note, want[i])
		}
	}
}

func TestPendingItemsAreOldestFirst(t *testing.T) {
	// The queue is a queue: whoever left something first is looked at first.
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	for i, note := range []string{"first", "second", "third"} {
		if err := s.CreateItem(ctx, lib.ID, makeItem(t, lib, note, base.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PendingItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Note != want {
			t.Errorf("position %d = %q, want %q", i, got[i].Note, want)
		}
	}
}

func TestItemsNeverCrossLibraries(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	a := makeLibrary(t)
	b := makeLibrary(t)
	b.Slug = "other"
	bID, _ := boulevard.NewLibraryID(rand.Reader)
	b.ID = bID
	for _, lib := range []boulevard.Library{a, b} {
		if err := s.CreateLibrary(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}
	mine := makeItem(t, a, "mine", time.Now().UTC())
	if err := s.CreateItem(ctx, a.ID, mine); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ItemByID(ctx, b.ID, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound — an item must not be readable through another library", err)
	}
	pending, err := s.PendingItems(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("other library's queue has %d items, want 0", len(pending))
	}
}

func TestCreateItemRejectsAForeignItem(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	it := makeItem(t, lib, "x", time.Now().UTC())
	it.LibraryID = boulevard.LibraryID("SOMEONEELSE")
	if err := s.CreateItem(ctx, lib.ID, it); err == nil {
		t.Error("creating an item for another library succeeded, want an error")
	}
}

func TestIncrementViews(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	it := makeItem(t, lib, "seen", time.Now().UTC())
	if err := s.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.IncrementViews(ctx, lib.ID, it.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Views != 3 {
		t.Errorf("Views = %d, want 3", got.Views)
	}
	if got.Takes != 0 {
		t.Errorf("Takes = %d, want 0 — nothing can take until Milestone 3", got.Takes)
	}
}

func TestIncrementViewsUnknownItemIsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementViews(ctx, lib.ID, "NOSUCHITEM"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestIncrementViewsAcrossLibrariesIsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	a := makeLibrary(t)
	b := makeLibrary(t)
	b.Slug = "other"
	bID, _ := boulevard.NewLibraryID(rand.Reader)
	b.ID = bID
	for _, lib := range []boulevard.Library{a, b} {
		if err := s.CreateLibrary(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}
	it := makeItem(t, a, "mine", time.Now().UTC())
	if err := s.CreateItem(ctx, a.ID, it); err != nil {
		t.Fatal(err)
	}

	if err := s.IncrementViews(ctx, b.ID, it.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound — an item must not be reachable through another library", err)
	}
	got, err := s.ItemByID(ctx, a.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Views != 0 {
		t.Errorf("Views = %d, want 0 — a cross-library call must touch nothing", got.Views)
	}
}

func shelveN(t *testing.T, s *Store, lib boulevard.Library, n int, base time.Time) []boulevard.Item {
	t.Helper()
	ctx := context.Background()
	var out []boulevard.Item
	for i := 0; i < n; i++ {
		it := makeItem(t, lib, fmt.Sprintf("item %d", i), base.Add(time.Duration(i)*time.Minute))
		if err := s.CreateItem(ctx, lib.ID, it); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApproveItem(ctx, lib.ID, it.ID, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

func TestApproveShelvesWithDefaultCopies(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	lib, _ = s.LibraryBySlug(ctx, lib.Slug) // pick up the §3 defaults
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	it := makeItem(t, lib, "approve me", now)
	if err := s.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	evicted, err := s.ApproveItem(ctx, lib.ID, it.ID, now)
	if err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	if evicted != "" {
		t.Errorf("evicted %q, want nothing on an empty shelf", evicted)
	}

	got, err := s.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %q, want shelved", got.State)
	}
	if got.CopiesTotal != lib.DefaultCopies || got.CopiesLeft != lib.DefaultCopies {
		t.Errorf("copies = %d/%d, want %d", got.CopiesLeft, got.CopiesTotal, lib.DefaultCopies)
	}
	if got.ShelvedAt == nil || !got.ShelvedAt.Equal(now) {
		t.Errorf("ShelvedAt = %v, want %v", got.ShelvedAt, now)
	}
}

func TestApproveIntoAFullShelfShedsTheOldest(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := smallShelf(t, s, 3)
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)

	first := shelveN(t, s, lib, 3, base)

	newcomer := makeItem(t, lib, "the newcomer", base.Add(time.Hour))
	if err := s.CreateItem(ctx, lib.ID, newcomer); err != nil {
		t.Fatal(err)
	}
	evicted, err := s.ApproveItem(ctx, lib.ID, newcomer.ID, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	if evicted != first[0].ID {
		t.Errorf("evicted %q, want the oldest %q", evicted, first[0].ID)
	}

	shelf, err := s.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 3 {
		t.Errorf("shelf holds %d, want it capped at 3", len(shelf))
	}
	oldest, err := s.ItemByID(ctx, lib.ID, first[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldest.State != boulevard.ItemShed {
		t.Errorf("oldest state = %q, want shed", oldest.State)
	}
}

func TestApproveIsAtomicWhenEvicting(t *testing.T) {
	// Approving a nonexistent item into a full shelf must shed nothing:
	// a shed item with no replacement would silently shrink the shelf.
	ctx, s := context.Background(), openTemp(t)
	lib := smallShelf(t, s, 2)
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	shelveN(t, s, lib, 2, base)

	if _, err := s.ApproveItem(ctx, lib.ID, "NOSUCHITEM", base.Add(time.Hour)); err == nil {
		t.Fatal("approving a missing item succeeded, want an error")
	}
	shelf, err := s.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 2 {
		t.Errorf("shelf holds %d after a failed approval, want 2 — nothing may be shed", len(shelf))
	}
}

func TestApproveRefusesAnItemThatIsNotPending(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	lib, _ = s.LibraryBySlug(ctx, lib.Slug)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	it := makeItem(t, lib, "twice", now)
	if err := s.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveItem(ctx, lib.ID, it.ID, now); err == nil {
		t.Error("approving an already-shelved item succeeded, want an error")
	}
}

// TestApproveReadsTheShelfSettingsFromTheDatabase is the regression for a
// capacity read that used to come from the caller. CreateLibrary does not
// write the settings columns, so `lib` here carries Slots: 0 and
// DefaultCopies: 0 — the exact value a caller would have on hand right after
// creating a library. Trusting it made `shelved >= slots` true on an empty
// shelf, which sheds a live item and hands back three copies of nothing.
func TestApproveReadsTheShelfSettingsFromTheDatabase(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if lib.Slots != 0 || lib.DefaultCopies != 0 {
		t.Fatalf("the fixture must carry unset settings, got slots %d copies %d", lib.Slots, lib.DefaultCopies)
	}
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	first := makeItem(t, lib, "already here", now)
	if err := s.CreateItem(ctx, lib.ID, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveItem(ctx, lib.ID, first.ID, now); err != nil {
		t.Fatal(err)
	}
	second := makeItem(t, lib, "room for this", now.Add(time.Minute))
	if err := s.CreateItem(ctx, lib.ID, second); err != nil {
		t.Fatal(err)
	}
	evicted, err := s.ApproveItem(ctx, lib.ID, second.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	if evicted != "" {
		t.Errorf("evicted %q with ten free slots; capacity must come from the database", evicted)
	}
	got, err := s.ItemByID(ctx, lib.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("the first item is %q, want it still shelved", got.State)
	}
	shelved, err := s.ItemByID(ctx, lib.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shelved.CopiesTotal != 3 || shelved.CopiesLeft != 3 {
		t.Errorf("copies = %d/%d, want the database's default of 3",
			shelved.CopiesLeft, shelved.CopiesTotal)
	}
}

func TestApproveForAnUnknownLibraryIsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	if _, err := s.ApproveItem(ctx, boulevard.LibraryID("NOPE"), "NOSUCHITEM", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestApproveAndRejectDistinguishMissingFromDecided keeps the two errors
// apart. RejectItem could not tell them apart at all while it inferred the
// outcome from RowsAffected == 0, so a mistyped id and a second `reject` on
// the same item read identically.
func TestApproveAndRejectDistinguishMissingFromDecided(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	shelved := makeItem(t, lib, "already decided", now)
	if err := s.CreateItem(ctx, lib.ID, shelved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveItem(ctx, lib.ID, shelved.ID, now); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		err    error
		want   error
		unwant error
	}{
		{"approve a missing item", approveErr(s, lib, "NOSUCHITEM", now), ErrNotFound, ErrNotPending},
		{"approve a decided item", approveErr(s, lib, shelved.ID, now), ErrNotPending, ErrNotFound},
		{"reject a missing item", s.RejectItem(ctx, lib.ID, "NOSUCHITEM"), ErrNotFound, ErrNotPending},
		{"reject a decided item", s.RejectItem(ctx, lib.ID, shelved.ID), ErrNotPending, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.want) {
				t.Errorf("err = %v, want %v", tc.err, tc.want)
			}
			if errors.Is(tc.err, tc.unwant) {
				t.Errorf("err = %v, which also reads as %v; the two must be distinguishable", tc.err, tc.unwant)
			}
		})
	}

	// The failed reject must not have touched it.
	got, err := s.ItemByID(ctx, lib.ID, shelved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %q, want it still shelved", got.State)
	}
}

func approveErr(s *Store, lib boulevard.Library, itemID string, now time.Time) error {
	_, err := s.ApproveItem(context.Background(), lib.ID, itemID, now)
	return err
}

// pendingTestItem creates and inserts a pending item, for tests that need
// one already in the store rather than just constructed in memory.
func pendingTestItem(t *testing.T, st *Store, id boulevard.LibraryID, itemID string, now time.Time) boulevard.Item {
	t.Helper()
	it := boulevard.Item{
		ID: itemID, LibraryID: id, Type: boulevard.ItemLink,
		Payload: "https://example.com/" + itemID, Note: "note for " + itemID,
		State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(context.Background(), id, it); err != nil {
		t.Fatalf("create pending item %s: %v", itemID, err)
	}
	return it
}

func TestApproveItemRecordsWhyItEvicted(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}

	first := shelvedTestItemAt(t, st, lib.ID, "FIRST", now.Add(-time.Hour))
	second := pendingTestItem(t, st, lib.ID, "SECOND", now)

	evicted, err := st.ApproveItem(context.Background(), lib.ID, second.ID, now)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if evicted != first.ID {
		t.Fatalf("evicted %q, want %q", evicted, first.ID)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, first.ID)
	if err != nil {
		t.Fatalf("read evicted item: %v", err)
	}
	if got.ShedReason != boulevard.ShedEvicted {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedEvicted)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set on the evicted item")
	}
}

func TestRejectReleasesWithoutDeleting(t *testing.T) {
	// §5 calls release a soft delete: a steward who rejects the wrong thing
	// has not destroyed it.
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	it := makeItem(t, lib, "no thanks", time.Now().UTC())
	if err := s.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectItem(ctx, lib.ID, it.ID); err != nil {
		t.Fatalf("RejectItem: %v", err)
	}
	got, err := s.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("the row must survive a rejection: %v", err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got.State)
	}
	pending, _ := s.PendingItems(ctx, lib.ID)
	if len(pending) != 0 {
		t.Errorf("queue still holds %d, want 0", len(pending))
	}
}

// TestApproveRefusesWhenEveryShelvedItemIsPinned covers the branch that
// Milestone 4a switches on. Before pins could be set, ApproveItem's eviction
// query could never match nothing; now it can, and the answer is a refusal
// rather than evicting a pin.
func TestApproveRefusesWhenEveryShelvedItemIsPinned(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	pinned := shelvedTestItemAt(t, st, lib.ID, "PINNED", now.Add(-time.Hour))
	if err := st.PinItem(ctx, lib.ID, pinned.ID, 3); err != nil {
		t.Fatalf("pin: %v", err)
	}
	waiting := pendingTestItem(t, st, lib.ID, "WAITING", now)

	_, err := st.ApproveItem(ctx, lib.ID, waiting.ID, now)
	if !errors.Is(err, ErrAllPinned) {
		t.Fatalf("approve error = %v, want ErrAllPinned", err)
	}

	// Nothing moved: not the pin, and not the item that was refused.
	got, err := st.ItemByID(ctx, lib.ID, pinned.ID)
	if err != nil {
		t.Fatalf("read pinned: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("the pinned item was evicted: state = %s", got.State)
	}
	still, err := st.ItemByID(ctx, lib.ID, waiting.ID)
	if err != nil {
		t.Fatalf("read waiting: %v", err)
	}
	if still.State != boulevard.ItemPending {
		t.Errorf("the refused item changed state to %s", still.State)
	}
}

func TestPinLimitIsThree(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	for i, id := range []string{"P1", "P2", "P3"} {
		it := shelvedTestItemAt(t, st, lib.ID, id, now.Add(time.Duration(i)*time.Minute))
		if err := st.PinItem(ctx, lib.ID, it.ID, 3); err != nil {
			t.Fatalf("pin %s: %v", id, err)
		}
	}
	fourth := shelvedTestItemAt(t, st, lib.ID, "P4", now)

	if err := st.PinItem(ctx, lib.ID, fourth.ID, 3); !errors.Is(err, ErrPinLimit) {
		t.Fatalf("fourth pin error = %v, want ErrPinLimit", err)
	}
	got, err := st.ItemByID(ctx, lib.ID, fourth.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Pinned {
		t.Error("the fourth item was pinned anyway")
	}
}

func TestUnpinFreesASlotForAnotherPin(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	first := shelvedTestItemAt(t, st, lib.ID, "P1", now)
	if err := st.PinItem(ctx, lib.ID, first.ID, 1); err != nil {
		t.Fatalf("pin: %v", err)
	}
	second := shelvedTestItemAt(t, st, lib.ID, "P2", now)
	if err := st.PinItem(ctx, lib.ID, second.ID, 1); !errors.Is(err, ErrPinLimit) {
		t.Fatalf("second pin error = %v, want ErrPinLimit", err)
	}

	if err := st.UnpinItem(ctx, lib.ID, first.ID); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if err := st.PinItem(ctx, lib.ID, second.ID, 1); err != nil {
		t.Fatalf("pin after unpin: %v", err)
	}
}

// TestRemoveShedsWithItsOwnReason also covers the RemoveItem invariant a
// plain "remove an unpinned item" case cannot: RemoveItem is the fourth and
// last exit from `shelved` (alongside ApproveItem's eviction,
// SweepExpiredItems, and TakeItem's take-to-zero, which all refuse or
// filter pinned items outright), so it is the only place that clears the
// flag. Deleting `, pinned = 0` from its UPDATE would leave a plain
// remove-of-an-unpinned-item test green while letting a pinned item survive
// a remove/reshelve round trip still pinned — which would let the shelf
// hold four pins and make PinItem wrongly refuse a legitimate fourth.
func TestRemoveShedsWithItsOwnReason(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.PinItem(ctx, lib.ID, it.ID, 3); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := st.RemoveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — remove sheds, it does not release", got.State)
	}
	if got.ShedReason != boulevard.ShedRemoved {
		t.Errorf("reason = %q, want %q", got.ShedReason, boulevard.ShedRemoved)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set")
	}
	if got.Pinned {
		t.Error("the item is still pinned in the shed — a pin must not exist off the shelf")
	}

	// Recoverable, which is why remove sheds rather than releases.
	if err := st.ReshelveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Errorf("a removed item could not be re-shelved: %v", err)
	}
	back, err := st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read after reshelve: %v", err)
	}
	if back.Pinned {
		t.Error("the pin came back on reshelve — it must return as ordinary stock")
	}
}

// TestPinUnpinRemoveRefuseItemsNotOnTheShelf checks both halves spec §10
// asks for: an id that exists but is not shelved gets ErrNotShelved, and an
// id naming nothing at all still gets ErrNotFound — the same "never
// existed" vs. "does not apply" split ErrNotPending and ErrNotShed already
// give the queue and the shed.
func TestPinUnpinRemoveRefuseItemsNotOnTheShelf(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()
	pending := pendingTestItem(t, st, lib.ID, "WAITING", now)

	if err := st.PinItem(ctx, lib.ID, pending.ID, 3); !errors.Is(err, ErrNotShelved) {
		t.Errorf("pin error = %v, want ErrNotShelved", err)
	}
	if err := st.UnpinItem(ctx, lib.ID, pending.ID); !errors.Is(err, ErrNotShelved) {
		t.Errorf("unpin error = %v, want ErrNotShelved", err)
	}
	if err := st.RemoveItem(ctx, lib.ID, pending.ID, now); !errors.Is(err, ErrNotShelved) {
		t.Errorf("remove error = %v, want ErrNotShelved", err)
	}

	if err := st.PinItem(ctx, lib.ID, "NOSUCHITEM", 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("pin error = %v, want ErrNotFound", err)
	}
	if err := st.UnpinItem(ctx, lib.ID, "NOSUCHITEM"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unpin error = %v, want ErrNotFound", err)
	}
	if err := st.RemoveItem(ctx, lib.ID, "NOSUCHITEM", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove error = %v, want ErrNotFound", err)
	}
}
