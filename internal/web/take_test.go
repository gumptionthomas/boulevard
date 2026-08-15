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
	// "You've taken three things with this scan." is a prefix of both the
	// refusal's Notice and the shared takecontrol block's own at-limit
	// hint, and both render in this body — so pinning on that alone would
	// pass even if the wrong one fired. "Scan the card again for more." is
	// unique to the refusal's Notice text (take.go's ErrLimitReached
	// branch), so it is what actually distinguishes the two (M-7).
	if !strings.Contains(rec.Body.String(), "Scan the card again for more.") {
		t.Error("the 403 does not carry the session-limit refusal's own explanation")
	}
}

func TestUntakeOnAnItemNeverTakenIsANoOp(t *testing.T) {
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

// TestTakeRaceToTheLastCopyGetsTheFriendlyMessage is I-2: TakeItem sheds an
// item the instant it reaches zero, in the same transaction as the
// decrement, so the genuine "someone took the last one" race — B taps take
// just after A took the last copy — leaves the item shed by the time B's
// request reaches TakeItem, which reports ErrNotFound rather than
// ErrNoCopiesLeft. Before this fix that fell through to a bare 404;
// handleTake now re-reads the item on ErrNotFound and recognizes shed-taken
// specifically.
func TestTakeRaceToTheLastCopyGetsTheFriendlyMessage(t *testing.T) {
	// A already took the item to zero inside bandFixture.
	h, lib, _, cookieB, itemID := bandFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookieB)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Someone took the last one.") {
		t.Error("the race to the last copy did not get the friendly message")
	}
}

// TestTakeWithNoCopiesGetsCopyTrueForZeroCopies is I-2's other half: the
// only way to reach ErrNoCopiesLeft today is default_copies = 0, where
// "someone took the last one" is simply untrue — the item was never
// takeable at all.
func TestTakeWithNoCopiesGetsCopyTrueForZeroCopies(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	stored, err := st.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.DefaultCopies = 0
	if err := st.UpdateLibrary(ctx, stored); err != nil {
		t.Fatal(err)
	}

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.com/thing", Note: "never had any copies",
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
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	h := New(st, func() time.Time { return now }).Handler()
	req := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+id+"/take", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "There are no copies of this to take.") {
		t.Error("a default_copies = 0 item did not get copy that is true for that case")
	}
	if strings.Contains(rec.Body.String(), "Someone took the last one.") {
		t.Error("a default_copies = 0 item claimed someone took the last one, which is untrue")
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

	// M-6: strings.Contains(body, "Take") also matches the inert
	// <span class="inert">Take</span>, so it would pass even if session
	// detection on this page broke entirely and the control never went
	// live. Asserting on the form's action is what actually proves an
	// active, submittable take control is present.
	want := `action="/b/` + lib.Slug + `/i/` + itemID + `/take"`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("the item page does not offer an active take control with a live session; want %q in body:\n%s", want, rec.Body.String())
	}
}

// TestItemPageWithoutASessionExplainsTheRule is I-3: handleItem never set
// pageData.HasSession, so item.html had no way to show the §6 explanation a
// remote reader gets on the shelf. Without it, a no-session item page showed
// a greyed "Take" control and nothing telling the reader why — indistinguishable
// from a broken feature.
func TestItemPageWithoutASessionExplainsTheRule(t *testing.T) {
	srv, lib, _, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/i/"+itemID, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "Scan the code at the box") {
		t.Error("the item page's inert control has no explanation without a session")
	}
}

// TestPinnedItemOffersNoTakeControl is I-4. ShelvedItems has no pinned
// filter, so a pinned item does render on the shelf, and only the template's
// {{if .Takeable}} guard keeps a control off it — untested until now, so a
// regression dropping that guard would put a live Take button on furniture
// (§5: a pin is furniture, not stock) and ship silently. Exercised with a
// live session, the state most likely to show an active button if the
// guard broke.
func TestPinnedItemOffersNoTakeControl(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.com/thing", Note: "furniture, not stock",
		State: boulevard.ItemPending, LeftAt: now, Pinned: true,
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
	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "furniture, not stock") {
		t.Fatal("the pinned item itself did not render on the shelf")
	}
	for _, forbidden := range []string{"Take</button>", `class="inert">Take`, "Put it back"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("a pinned item rendered a take control (found %q) — a pin is furniture, not stock", forbidden)
		}
	}
}

// bandFixture is I-1's setup: a library whose default_copies is 1 (so one
// take empties the item), one shelved item, and two live sessions. Session
// A takes the item to zero, which sheds it — the item now exists only in
// A's take-to-zero band. Session B never touches it.
func bandFixture(t *testing.T) (h http.Handler, lib boulevard.Library, cookieA, cookieB *http.Cookie, itemID string) {
	t.Helper()
	ctx := context.Background()
	st := testStore(t)
	lib = addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	stored, err := st.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.DefaultCopies = 1
	if err := st.UpdateLibrary(ctx, stored); err != nil {
		t.Fatal(err)
	}

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.com/thing", Note: "the last copy of this",
		State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(ctx, lib.ID, it); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	// One token, reused by both sessions: addAnyToken always mints
	// period_index 1, and tokens carries a UNIQUE(library_id, period_index)
	// constraint, so a second call for the same library fails. Nothing
	// about sessions.token_id requires distinct tokens — the same card
	// scanned twice mints two sessions — so reusing it is fine here, the
	// same way TestUntakeByANonTakerOnAFullShelfIsANoOp does in the store
	// package's own tests.
	tok := addAnyToken(t, st, lib)

	sidA, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sidA, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	sidB, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, boulevard.Session{
		ID: sidB, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	h = New(st, func() time.Time { return now }).Handler()
	cookieA = &http.Cookie{Name: cookieName, Value: string(sidA)}
	cookieB = &http.Cookie{Name: cookieName, Value: string(sidB)}

	take := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+id+"/take", nil)
	take.AddCookie(cookieA)
	h.ServeHTTP(httptest.NewRecorder(), take)

	return h, lib, cookieA, cookieB, id
}

// TestBandOffersPutItBackAndRestoresTheItem is I-1's first required test:
// the session that took the last copy sees the item in the band with
// "Put it back", and pressing it restores the item to the live shelf.
func TestBandOffersPutItBackAndRestoresTheItem(t *testing.T) {
	h, lib, cookieA, _, itemID := bandFixture(t)

	shelf := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	shelf.AddCookie(cookieA)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, shelf)
	body := rec.Body.String()
	if !strings.Contains(body, "the last copy of this") {
		t.Fatal("the taker's own emptied item is not shown in the band")
	}
	if !strings.Contains(body, "Put it back") {
		t.Fatal("the band does not offer the undo")
	}
	// The empty-state copy must not claim there is nothing here (I-1): the
	// live list really is empty, but the band right below is not.
	if strings.Contains(body, "That's an invitation.") {
		t.Error("the ordinary empty-shelf invitation rendered even though the band has this session's item")
	}

	undo := httptest.NewRequest(http.MethodPost, "/b/"+lib.Slug+"/i/"+itemID+"/untake", nil)
	undo.AddCookie(cookieA)
	undoRec := httptest.NewRecorder()
	h.ServeHTTP(undoRec, undo)
	if undoRec.Code != http.StatusSeeOther {
		t.Fatalf("undo status = %d, want 303", undoRec.Code)
	}

	shelf2 := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	shelf2.AddCookie(cookieA)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, shelf2)
	body2 := rec2.Body.String()
	if !strings.Contains(body2, `action="/b/`+lib.Slug+`/i/`+itemID+`/take"`) {
		t.Error("the item did not return to the live shelf with an active take control after the undo")
	}
	if strings.Contains(body2, "Put it back") {
		t.Error("the band still shows the item after it was put back")
	}
}

// TestBandNeverAppearsWithoutASession is I-1's hard requirement: consumption
// is global, mutation is local, so this band must never appear to a reader
// with no session at all.
func TestBandNeverAppearsWithoutASession(t *testing.T) {
	h, lib, _, _, _ := bandFixture(t)

	rec := get(t, h, "/b/"+lib.Slug+"/")
	body := rec.Body.String()
	if strings.Contains(body, "the last copy of this") {
		t.Error("the band appeared to a reader with no session")
	}
	if strings.Contains(body, "Put it back") {
		t.Error("an undo control appeared to a reader with no session")
	}
	if strings.Contains(body, "You took the last one") {
		t.Error("the band's label appeared to a reader with no session")
	}
}

// TestBandNeverShowsAnotherSessionsTake is I-1's other hard requirement:
// the band must never show a different session's takes.
func TestBandNeverShowsAnotherSessionsTake(t *testing.T) {
	h, lib, _, cookieB, _ := bandFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	req.AddCookie(cookieB)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "the last copy of this") {
		t.Error("session B saw session A's taken-to-zero item")
	}
	if strings.Contains(body, "Put it back") {
		t.Error("session B was offered an undo for session A's take")
	}
	if strings.Contains(body, "You took the last one") {
		t.Error("the band's label appeared for a session that took nothing to zero")
	}
}
