package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// shelveOne leaves an item and approves it, so it is on the public shelf.
func shelveOne(t *testing.T, st *store.Store, lib boulevard.Library, note, payload string, now time.Time) boulevard.Item {
	t.Helper()
	ctx := context.Background()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink, Payload: payload,
		Note: note, State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	full, err := st.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, full, it.ID, now); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestShelfShowsItsItemsNoteFirst(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	shelveOne(t, st, lib, "Reminded me of the alley cat.", "https://www.youtube.com/watch?v=x", now)

	rec := get(t, New(st, func() time.Time { return now }).Handler(), "/b/fairview/")
	body := rec.Body.String()
	if !strings.Contains(body, "Reminded me of the alley cat.") {
		t.Error("the note is missing from the shelf")
	}
	// Domain only: nothing is ever fetched, and a hostile URL cannot borrow
	// a trustworthy title.
	if !strings.Contains(body, "youtube.com") {
		t.Error("the link's domain is missing")
	}
	if strings.Contains(body, "watch?v=x") {
		t.Error("the full URL leaked onto the shelf; the spec says domain only")
	}
}

func TestShelfShowsTheCopyCount(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	shelveOne(t, st, lib, "a reason", "https://example.org/a", now)

	body := get(t, New(st, func() time.Time { return now }).Handler(), "/b/fairview/").Body.String()
	if !strings.Contains(body, "3 copies") {
		t.Error("§6 requires copies_left on every item")
	}
}

func TestShelfOmitsPendingItems(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	id, _ := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err := st.CreateItem(context.Background(), lib.ID, boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.org/secret", Note: "not approved yet",
		State: boulevard.ItemPending, LeftAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	body := get(t, New(st, func() time.Time { return now }).Handler(), "/b/fairview/").Body.String()
	if strings.Contains(body, "not approved yet") {
		t.Error("a pending item appeared on the public shelf")
	}
}

func TestItemPageRendersAndCountsAView(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	it := shelveOne(t, st, lib, "worth sharing", "https://example.org/a", now)

	h := New(st, func() time.Time { return now }).Handler()
	rec := get(t, h, "/b/fairview/i/"+it.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "worth sharing") {
		t.Error("the item page does not show the note")
	}
	// §7: the counters are not displayed until takes can move.
	if strings.Contains(rec.Body.String(), "Taken 0 times") {
		t.Error("a take count was displayed; nothing can take until Milestone 3")
	}

	get(t, h, "/b/fairview/i/"+it.ID)
	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Views != 2 {
		t.Errorf("Views = %d, want 2", got.Views)
	}
}

func TestItemPage404sAcrossLibraries(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	addLibrary(t, st, "whittier")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	it := shelveOne(t, st, lib, "mine", "https://example.org/a", now)

	rec := get(t, New(st, func() time.Time { return now }).Handler(), "/b/whittier/i/"+it.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — an item is not readable through another library", rec.Code)
	}
}
