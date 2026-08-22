package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// expiredItemServer builds a library (max_age_days defaults to 90) with one
// item shelved 200 days before now — past the boundary — so both the shelf
// and the item page have something to sweep before they render.
func expiredItemServer(t *testing.T) (http.Handler, boulevard.Library, string) {
	t.Helper()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	shelvedAt := now.AddDate(0, 0, -200)

	it := leaveOne(t, st, lib, "long expired", "https://example.org/old", shelvedAt)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, shelvedAt); err != nil {
		t.Fatal(err)
	}

	h := New(st, func() time.Time { return now }).Handler()
	return h, lib, it.ID
}

func TestShelfSweepsBeforeItRenders(t *testing.T) {
	// An item shelved 200 days ago on a 90-day library must not appear.
	srv, lib, _ := expiredItemServer(t)

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "long expired") {
		t.Error("the shelf rendered an item past max_age_days")
	}
}

func TestItemPageSweepsToo(t *testing.T) {
	// The gap that is easy to miss: an unswept item is still `shelved`, so
	// the item handler's state filter passes it and the shareable URL
	// outlives the shelf listing.
	srv, lib, itemID := expiredItemServer(t)

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/i/"+itemID, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — an expired item still serves at its own URL", rec.Code)
	}
}

// Final review, F5: SweepExpiredItems ran from the public shelf and item
// handlers and nowhere a steward looked, so on a quiet box (DESIGN.md's
// typical case) a steward page could report an item as still shelved —
// with live Pin and Remove controls on it — for as long as nobody loaded
// the public shelf. Modeled directly on TestShelfSweepsBeforeItRenders
// above, but through requireSteward instead of the public path.
func TestStewardShelfSweepsBeforeItRenders(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	shelvedAt := now.AddDate(0, 0, -200)

	it := leaveOne(t, st, lib, "long expired", "https://example.org/old", shelvedAt)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, shelvedAt); err != nil {
		t.Fatal(err)
	}
	key := armWithStewardKey(t, st, lib)

	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/shelf", c)
	if strings.Contains(rec.Body.String(), "long expired") {
		t.Error("the steward shelf page rendered an item past max_age_days")
	}
}
