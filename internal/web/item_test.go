package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// leaveOne creates an item and leaves it exactly where CreateItem put it —
// pending — for a test that drives it to a different state itself.
func leaveOne(t *testing.T, st *store.Store, lib boulevard.Library, note, payload string, now time.Time) boulevard.Item {
	t.Helper()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink, Payload: payload,
		Note: note, State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(context.Background(), lib.ID, it); err != nil {
		t.Fatal(err)
	}
	return it
}

// shelveOne leaves an item and approves it, so it is on the public shelf.
func shelveOne(t *testing.T, st *store.Store, lib boulevard.Library, note, payload string, now time.Time) boulevard.Item {
	t.Helper()
	ctx := context.Background()
	it := leaveOne(t, st, lib, note, payload, now)
	if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
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
	// §7: takes are displayed on the item page; an untaken item reads
	// "Taken 0 times" rather than omitting the line.
	if !strings.Contains(rec.Body.String(), "Taken 0 times") {
		t.Error("the item page does not display its take count")
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

// TestItemPageDisplaysTakeCountAfterTwoTakes is F2: DESIGN.md §7 says item
// pages display takes as e.g. "Taken 3 times", and this is the milestone
// where takes can move. Two sessions each take a copy (copies_total
// defaults to 3, so the item survives both), and the item's own page must
// show the count.
func TestItemPageDisplaysTakeCountAfterTwoTakes(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	it := shelveOne(t, st, lib, "worth taking", "https://example.org/a", now)

	// One token, reused by both sessions: addAnyToken always mints
	// period_index 1 and tokens carries a UNIQUE(library_id, period_index)
	// constraint, so a second call for the same library fails. Nothing
	// about sessions.token_id requires distinct tokens.
	tok := addAnyToken(t, st, lib)
	h := New(st, func() time.Time { return now }).Handler()

	for _, id := range []string{"A", "B"} {
		sid, err := boulevard.NewSessionID(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSession(ctx, boulevard.Session{
			ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
			CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
		}); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/b/fairview/i/"+it.ID+"/take", nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("take by %s: status = %d, want 303", id, rec.Code)
		}
	}

	rec := get(t, h, "/b/fairview/i/"+it.ID)
	if !strings.Contains(rec.Body.String(), "Taken 2 times") {
		t.Errorf("the item page does not show the take count; body:\n%s", rec.Body.String())
	}
}

// TestItemPageOnlyRendersShelvedItems covers the approval gate at the item
// page itself, not just the shelf listing. ItemByID is library-scoped but
// not state-scoped — the CLI and a future admin view need to load an item
// regardless of its state — so the handler is what must refuse to serve a
// pending, shed or released item's own URL. Without this, an item's id
// (128 bits of randomness, so not practically guessable, but still wrong)
// would make it public before approval, or after eviction, or after
// rejection — all three of which DESIGN.md §5 calls not public.
func TestItemPageOnlyRendersShelvedItems(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()

	t.Run("pending", func(t *testing.T) {
		it := leaveOne(t, st, lib, "not approved yet", "https://example.org/a", now)
		rec := get(t, h, "/b/fairview/i/"+it.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 — a pending item's own page is the approval gate", rec.Code)
		}
	})

	t.Run("shed", func(t *testing.T) {
		// Reach `shed` through the real eviction path rather than writing the
		// state directly: narrow this library's shelf to one slot, approve a
		// first item onto it, then approve a second — ApproveItem's own FIFO
		// eviction (internal/store/item.go) moves the first to `shed`.
		ctx := context.Background()
		full, err := st.LibraryBySlug(ctx, lib.Slug)
		if err != nil {
			t.Fatal(err)
		}
		full.Slots = 1
		if err := st.UpdateLibrary(ctx, full); err != nil {
			t.Fatal(err)
		}
		first := leaveOne(t, st, lib, "sheddable", "https://example.org/b", now)
		if _, err := st.ApproveItem(ctx, lib.ID, first.ID, now); err != nil {
			t.Fatal(err)
		}
		second := leaveOne(t, st, lib, "shelved instead", "https://example.org/c", now)
		evicted, err := st.ApproveItem(ctx, lib.ID, second.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		if evicted != first.ID {
			t.Fatalf("evicted = %q, want %q — the shed test needs the first item shed", evicted, first.ID)
		}

		rec := get(t, h, "/b/fairview/i/"+first.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 — §5 says a shed item is not public", rec.Code)
		}
	})

	t.Run("released", func(t *testing.T) {
		it := leaveOne(t, st, lib, "rejected", "https://example.org/c", now)
		if err := st.RejectItem(context.Background(), lib.ID, it.ID); err != nil {
			t.Fatal(err)
		}
		rec := get(t, h, "/b/fairview/i/"+it.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 — a released item is a soft delete, not a public page", rec.Code)
		}
	})
}

// TestItemPageOnUnknownIDRendersInvalidPage is F8: before this milestone a
// shelved item's URL never went stale, so a bare id that names nothing was
// the only way to reach this 404 and the stdlib's plain-text page was an
// acceptable stand-in. Now expiry sheds items on a schedule and a take can
// shed one in a tap, so someone following a shared link to an item that
// left the shelf last week is a routine case, not a typo — it must get the
// same page an unknown library slug gets, not "404 page not found" in the
// browser's default font.
func TestItemPageOnUnknownIDRendersInvalidPage(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	rec := get(t, New(st, func() time.Time { return now }).Handler(), "/b/"+lib.Slug+"/i/NOSUCHITEM")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "That code isn't valid.") {
		t.Errorf("body did not render invalid.html; got:\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "404 page not found") {
		t.Error("the stdlib's plain-text 404 leaked through")
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
