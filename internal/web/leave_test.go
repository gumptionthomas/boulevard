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
	if !strings.Contains(rec.Body.String(), "Scan the code at the box to take or leave something.") {
		t.Error("no-session form must carry the §6 explanation")
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
		"Left at the box.",
		"The steward looks at new things before they go on the shelf. Yours is waiting.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation missing %q", want)
		}
	}
}

func TestLeaveOnAnUnknownSlugIs404(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	if rec := get(t, New(st, time.Now).Handler(), "/b/nope/leave"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
