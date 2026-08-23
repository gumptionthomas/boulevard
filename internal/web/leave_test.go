package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// withSession gives the request a live session for lib.
func withSession(t *testing.T, st *store.Store, lib boulevard.Library, req *http.Request, now time.Time) *http.Request {
	t.Helper()
	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(context.Background(), boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	return req
}

func postLeave(t *testing.T, st *store.Store, slug string, form url.Values, now time.Time, sess *boulevard.Library) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/b/"+slug+"/leave", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		req = withSession(t, st, *sess, req, now)
	}
	rec := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)
	return rec
}

func goodForm() url.Values {
	return url.Values{
		"type":        {"link"},
		"payload":     {"https://example.org/thing"},
		"note":        {"Reminded me of the alley cat."},
		"attribution": {"the guy with the beagle"},
	}
}

func TestLeaveFormWithoutASessionIsInertNot404(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := get(t, New(st, time.Now).Handler(), "/b/fairview/leave")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — show the rule, do not hide the feature", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Scan the card at the shelf to take or leave.") {
		t.Error("no-session form must carry the §6 explanation")
	}
}

// TestLeaveFormAlwaysOffersAWayBack covers a dead end the acceptance run
// found and no test did: with the submit button disabled — no session, or
// three leaves already spent — the form had no exit but the browser's back
// gesture. Both other secondary pages have carried this link all along.
func TestLeaveFormAlwaysOffersAWayBack(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := get(t, New(st, time.Now).Handler(), "/b/fairview/leave")
	if !strings.Contains(rec.Body.String(), `href="/b/fairview/"`) {
		t.Error("the leave form offers no way back to the shelf")
	}
	if !strings.Contains(rec.Body.String(), "Back to the shelf") {
		t.Error("the way back is not labelled")
	}
}

func TestLeaveSubmitWithoutASessionIs403(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	rec := postLeave(t, st, "fairview", goodForm(), time.Now(), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	items, err := st.PendingItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Error("an item was stored without a session")
	}
}

// TestLeaveSubmit403KeepsTheComposedNote covers the 403 for the same reason
// the 422 path re-renders: DESIGN.md §4 sells the 24-hour window as "scan on
// your walk, write at your kitchen table", so a session expiring between
// loading the form and submitting it is the intended usage pattern hitting
// its boundary — not a rare accident. Handing back a blank form loses a
// composed note exactly as a validation error would.
func TestLeaveSubmit403KeepsTheComposedNote(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")

	form := goodForm()
	form.Set("note", "Reminded me of the alley cat, and of the rain.")
	form.Set("payload", "https://example.org/kept")
	form.Set("attribution", "the guy with the beagle")
	rec := postLeave(t, st, "fairview", form, time.Now(), nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Reminded me of the alley cat, and of the rain.",
		"https://example.org/kept",
		"the guy with the beagle",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the 403 form lost %q; the note is the part that took effort", want)
		}
	}
	// Still inert, which is what makes re-rendering it populated safe.
	if !strings.Contains(body, "Scan the card at the shelf to take or leave.") {
		t.Error("the 403 form must still carry the §6 explanation")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("the 403 form must still be inert")
	}
}

// TestLeaveSubmitCapsTheBody guards the only route that accepts a write from
// a stranger. Without a limit, ParseForm reads up to Go's 10 MB default into
// memory before any rune cap applies, and every request serializes through
// one SQLite connection.
func TestLeaveSubmitCapsTheBody(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	form := goodForm()
	form.Set("payload", strings.Repeat("x", maxLeaveBody))
	rec := postLeave(t, st, "fairview", form, now, &lib)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body over the cap", rec.Code)
	}
	items, err := st.PendingItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Error("an over-long submission was stored")
	}
}

// A submission that uses its whole allowance in a multi-byte script must
// still fit: the cap is bytes and the validation limits are runes.
func TestLeaveSubmitAcceptsEveryFieldAtItsLimit(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	form := goodForm()
	form.Set("type", "text")
	form.Set("payload", strings.Repeat("é", boulevard.MaxTextRunes))
	form.Set("note", strings.Repeat("é", boulevard.MaxNoteRunes))
	form.Set("attribution", strings.Repeat("é", boulevard.MaxAttributionRunes))
	if rec := postLeave(t, st, "fairview", form, now, &lib); rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 — every field is exactly at its rune limit", rec.Code)
	}
}

func TestLeaveSubmitWithAnotherLibrarysSessionIs403(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	other := addLibrary(t, st, "whittier")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	rec := postLeave(t, st, "fairview", goodForm(), now, &other)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — a session at one box grants nothing at another", rec.Code)
	}
}

func TestLeaveSubmitStoresAPendingItem(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	rec := postLeave(t, st, "fairview", goodForm(), now, &lib)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	items, err := st.PendingItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("stored %d items, want 1", len(items))
	}
	if items[0].Note != "Reminded me of the alley cat." {
		t.Errorf("Note = %q", items[0].Note)
	}
	if items[0].State != boulevard.ItemPending {
		t.Errorf("state = %q, want pending — approval is default on", items[0].State)
	}

	shelf, err := st.ShelvedItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 0 {
		t.Error("a freshly left item appeared on the public shelf")
	}
}

// TestLeaveWithApprovalOffShelvesImmediately covers the one setting
// DESIGN.md §5 calls steward-configurable. approval_required is stored,
// defaults on, and until now nothing read it — so a steward following
// docs/shelf-acceptance.md and editing `libraries` with a SQLite client got
// a silent no-op.
func TestLeaveWithApprovalOffShelvesImmediately(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	stored, err := st.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.ApprovalRequired = false
	if err := st.UpdateLibrary(ctx, stored); err != nil {
		t.Fatal(err)
	}

	if rec := postLeave(t, st, "fairview", goodForm(), now, &lib); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	shelf, err := st.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 1 {
		t.Fatalf("shelf holds %d items, want 1 — approval is off, so no CLI step should be needed", len(shelf))
	}
	if shelf[0].Note != "Reminded me of the alley cat." {
		t.Errorf("Note = %q", shelf[0].Note)
	}
	// Through ApproveItem, so the copies mechanic is not bypassed.
	if shelf[0].CopiesLeft != 3 || shelf[0].CopiesTotal != 3 {
		t.Errorf("copies = %d/%d, want the library's default of 3", shelf[0].CopiesLeft, shelf[0].CopiesTotal)
	}
	if shelf[0].ShelvedAt == nil {
		t.Error("ShelvedAt is nil on a shelved item")
	}
	pending, err := st.PendingItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("queue holds %d items, want 0", len(pending))
	}

	// And the confirmation must not claim a steward is going to look at it.
	body := get(t, New(st, func() time.Time { return now }).Handler(), "/b/fairview/left").Body.String()
	if strings.Contains(body, "The steward looks at new things") {
		t.Error("the confirmation says a steward looks first, but the item is already on the shelf")
	}
}

// The default is the one that matters most: §5 says approval is on unless a
// steward turns it off, and TestLeaveSubmitStoresAPendingItem above asserts
// the same thing from the other side.
func TestLeaveWithApprovalOnStaysPending(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	stored, err := st.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ApprovalRequired {
		t.Fatal("a fresh library has approval off; §5 says it defaults on")
	}

	postLeave(t, st, "fairview", goodForm(), now, &lib)

	pending, err := st.PendingItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("queue holds %d items, want 1", len(pending))
	}
	shelf, err := st.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelf) != 0 {
		t.Errorf("shelf holds %d items, want 0 — approval is on", len(shelf))
	}
}

func TestLeaveSubmitRejectsABlankNoteAndKeepsWhatWasTyped(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	form := goodForm()
	form.Set("note", "   ")
	form.Set("payload", "https://example.org/kept")
	rec := postLeave(t, st, "fairview", form, now, &lib)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://example.org/kept") {
		t.Error("the re-rendered form lost what was typed; losing a composed note is this form's worst failure")
	}
	items, _ := st.PendingItems(context.Background(), lib.ID)
	if len(items) != 0 {
		t.Error("an invalid submission was stored")
	}
}

// TestLeaveFormEscapesAClosingTextareaTag pins the one place where
// correctness rests entirely on a stdlib guarantee. html/template knows it
// is inside a <textarea> and escapes accordingly; text/template does not,
// and a refactor toward it would silently turn a re-rendered note into
// markup on the page of the next person who loads the form.
func TestLeaveFormEscapesAClosingTextareaTag(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	form := goodForm()
	form.Set("note", "") // force the 422 re-render
	form.Set("payload", "</textarea><script>alert(1)</script>")
	body := postLeave(t, st, "fairview", form, now, &lib).Body.String()

	if strings.Contains(body, "</textarea><script>") {
		t.Errorf("a closing textarea tag survived into the page:\n%s", body)
	}
	if !strings.Contains(body, "&lt;/textarea&gt;") {
		t.Errorf("the submitted text was not escaped back into the textarea:\n%s", body)
	}
}

func TestLeaveConfirmationDoesNotImplyItIsLive(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	postLeave(t, st, "fairview", goodForm(), now, &lib)

	rec := get(t, New(st, func() time.Time { return now }).Handler(), "/b/fairview/left")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Left at the shelf.",
		"The steward looks at new things before they go on the shelf. Yours is waiting.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation missing %q", want)
		}
	}
}

// TestLeaveSubmitAtSessionLimitIsRefusedAndExplained mirrors
// TestTakeAtTheSessionLimitIsRefusedAndExplained in take_test.go: same
// session id reused across every submission, since postLeave's sess
// parameter mints a fresh session (and fresh counter) each call.
func TestLeaveSubmitAtSessionLimitIsRefusedAndExplained(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(context.Background(), boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: cookieName, Value: string(sid)}

	postWithCookie := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/b/fairview/leave", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 3; i++ {
		form := goodForm()
		form.Set("payload", "https://example.org/"+string(rune('a'+i)))
		if rec := postWithCookie(form); rec.Code != http.StatusSeeOther {
			t.Fatalf("leave %d: status = %d, want 303", i, rec.Code)
		}
	}

	form := goodForm()
	form.Set("note", "The fourth thing, composed with care.")
	form.Set("payload", "https://example.org/kept")
	form.Set("attribution", "the guy with the beagle")
	rec := postWithCookie(form)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 at the session limit", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "You've left three things with this scan. Scan the card again for more.") {
		t.Error("the 403 does not explain the session limit")
	}
	for _, want := range []string{
		"The fourth thing, composed with care.",
		"https://example.org/kept",
		"the guy with the beagle",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the at-limit 403 lost %q; the note is the part that took effort", want)
		}
	}
	if !strings.Contains(body, "disabled") {
		t.Error("the at-limit form must still be inert")
	}

	items, err := st.PendingItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("stored %d items, want 3 — the fourth must not have been stored", len(items))
	}
}

func TestLeaveFormAtSessionLimitIsInertOnLoadToo(t *testing.T) {
	// The rule shows up whenever the form is displayed at the limit, not
	// only after a failed submit attempt — the same principle that keeps the
	// no-session case visible rather than hidden.
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tok := addAnyToken(t, st, lib)
	if err := st.CreateSession(context.Background(), boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxLeaves; i++ {
		if err := st.IncrementLeaves(context.Background(), sid); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/b/fairview/leave", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — show the rule, do not hide the feature", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "You've left three things with this scan. Scan the card again for more.") {
		t.Error("the form does not explain the session limit on a plain GET")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("the form must be inert on a plain GET at the limit")
	}
}

func TestLeaveOnAnUnknownSlugIs404(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	if rec := get(t, New(st, time.Now).Handler(), "/b/nope/leave"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
