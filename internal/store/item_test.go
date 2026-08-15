package store

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

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
