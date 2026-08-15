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
)

// takeServer builds a store, a library, a live session for it and one
// shelved item of the given type and payload, following the fixtures
// leave_test.go's withSession and item_test.go's shelveOne already
// established (testStore, addLibrary, addAnyToken, CreateSession,
// CreateItem, ApproveItem) rather than a parallel set.
//
// It returns http.Handler, not *Server: every other test in this package
// drives requests through New(...).Handler(), which wraps the mux in
// logRequests, and *Server itself has no ServeHTTP method. See
// task-6-report.md for why this return type differs from the brief's.
func takeServer(t *testing.T, typ boulevard.ItemType, payload string) (http.Handler, boulevard.Library, *http.Cookie, string) {
	t.Helper()
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: typ, Payload: payload,
		Note: "a reason", State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	h := New(st, func() time.Time { return now }).Handler()
	cookie := &http.Cookie{Name: cookieName, Value: string(sid)}
	return h, lib, cookie, it.ID
}

func TestTakeRedirectsToThePayload(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://example.com/thing" {
		t.Errorf("Location = %q, want the payload", got)
	}
}

func TestTakeOfTextRedirectsToTheShelfAnchor(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemText, "some words")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	want := "/b/" + lib.Slug + "/#i-" + itemID
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q — a text item has nothing to open", got, want)
	}
}

func TestTakeWithoutASessionIsRefused(t *testing.T) {
	srv, lib, _, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Scan the code at the box") {
		t.Error("the 403 does not explain the rule")
	}
}

func TestTakeIsPostOnly(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodGet,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusSeeOther {
		t.Fatal("GET take succeeded — that is a shareable, prefetchable open redirect")
	}
}

func TestUndoReturnsToTheShelf(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	take := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	take.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), take)

	undo := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/untake", nil)
	undo.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, undo)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := "/b/" + lib.Slug + "/#i-" + itemID
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestShelfShowsPutItBackAfterATake(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	take := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	take.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), take)

	shelf := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	shelf.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, shelf)

	body := rec.Body.String()
	if !strings.Contains(body, "Put it back") {
		t.Error("the shelf does not offer the undo after a take")
	}
}

// TestTakeWithAnotherLibrarysSessionIs403 mirrors
// TestLeaveSubmitWithAnotherLibrarysSessionIs403 in leave_test.go: a session
// minted at one box grants nothing at another, on the same host.
func TestTakeWithAnotherLibrarysSessionIs403(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	other := addLibrary(t, st, "whittier")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.com/thing", Note: "a reason",
		State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, other)
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sid, LibraryID: other.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	h := New(st, func() time.Time { return now }).Handler()
	req := httptest.NewRequest(http.MethodPost, "/b/fairview/i/"+id+"/take", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — a session at one box grants nothing at another", rec.Code)
	}
}

func TestTakeIsIdempotentOnRetry(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	for range 2 {
		req := httptest.NewRequest(http.MethodPost,
			"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
	}
}

func TestTakeAtTheSessionLimitIsRefusedAndExplained(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	var lastID string
	for i := 0; i < maxTakes+1; i++ {
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		it := boulevard.Item{
			ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
			Payload: "https://example.com/" + id, Note: "a reason",
			State: boulevard.ItemPending, LeftAt: now,
		}
		if err := st.CreateItem(ctx, lib.ID, it); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
			t.Fatal(err)
		}
		lastID = id
	}

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: cookieName, Value: string(sid)}
	h := New(st, func() time.Time { return now }).Handler()

	items, err := st.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == lastID {
			continue
		}
		req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+it.ID+"/take", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("take of %q: status = %d, want 303", it.ID, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+lastID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 at the session limit", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "You've taken three things") {
		t.Error("the 403 does not explain the session limit")
	}
}

func TestUntakeOnAnItemNeverTakenIsANoOpNotFound(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+itemID+"/untake", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 — UntakeItem's untaken case is a no-op, not a 404", rec.Code)
	}
}

func TestUntakeWithoutASessionIsRefused(t *testing.T) {
	srv, lib, _, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+itemID+"/untake", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestTakeOnAnUnknownItemIs404(t *testing.T) {
	srv, lib, cookie, _ := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/NOSUCHITEM/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestItemPageOffersTheTakeControl(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/i/"+itemID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Take") {
		t.Error("the item page does not offer a take control with a live session")
	}
}
