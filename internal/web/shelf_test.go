package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addLibrary(t *testing.T, st *store.Store, slug string) boulevard.Library {
	t.Helper()
	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lib := boulevard.Library{
		ID: id, Slug: slug, Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       "https://example.org",
	}
	if err := st.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	return lib
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// addToken seeds a real token row so a session referencing it satisfies the
// sessions.token_id foreign key (schema.sql). Not part of the task-6 brief's
// given test code — see task-6-report.md for why it was added.
func addToken(t *testing.T, st *store.Store, lib boulevard.Library, id string) boulevard.Token {
	t.Helper()
	tok := boulevard.Token{
		ID:          id,
		LibraryID:   lib.ID,
		Secret:      id + "-secret",
		PeriodIndex: 0,
		ValidFrom:   boulevard.NewDate(2026, time.January, 1),
		ValidUntil:  boulevard.NewDate(2026, time.December, 31),
		State:       boulevard.TokenActive,
	}
	if err := st.InsertTokens(context.Background(), lib.ID, []boulevard.Token{tok}); err != nil {
		t.Fatal(err)
	}
	return tok
}

// addAnyToken seeds one currently-valid token so a session has something
// real to reference — sessions.token_id is a foreign key. Unlike addToken
// above (which takes a caller-chosen id/secret for the scan-URL fixtures),
// callers here only need a valid token to exist, not a particular one, so
// the id and secret are generated.
func addAnyToken(t *testing.T, st *store.Store, lib boulevard.Library) boulevard.Token {
	t.Helper()
	secret, err := tokens.NewSecret(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	tok := boulevard.Token{
		ID: id, LibraryID: lib.ID, Secret: secret, PeriodIndex: 1,
		ValidFrom:  boulevard.NewDate(2026, time.August, 1),
		ValidUntil: boulevard.NewDate(2026, time.August, 31),
		State:      boulevard.TokenPending,
	}
	if err := st.InsertTokens(context.Background(), lib.ID, []boulevard.Token{tok}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestRootRedirectsToTheSoleLibrary(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := get(t, New(st, time.Now).Handler(), "/")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/b/fairview/" {
		t.Errorf("Location = %q, want %q", got, "/b/fairview/")
	}
}

func TestRootIs404WhenNoLibraries(t *testing.T) {
	rec := get(t, New(testStore(t), time.Now).Handler(), "/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRootIs404WithMoreThanOneLibrary(t *testing.T) {
	// Reachable today: `boulevard booklet` run twice with different names
	// creates two libraries in one file. A list would be the neighborhood
	// map, which is a v2 host surface (spec §5).
	st := testStore(t)
	addLibrary(t, st, "fairview")
	addLibrary(t, st, "whittier")
	rec := get(t, New(st, time.Now).Handler(), "/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestShelfRendersLibraryName(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := get(t, New(st, time.Now).Handler(), "/b/fairview/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "The Fairview Boulevard") {
		t.Error("body does not contain the library name")
	}
}

func TestShelfWithoutSessionExplainsTheRule(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	body := get(t, New(st, time.Now).Handler(), "/b/fairview/").Body.String()
	if !strings.Contains(body, "Scan the card at the shelf to take or leave.") {
		t.Error("no-session shelf must carry the §6 explanation")
	}
	if strings.Contains(body, "You're at the shelf.") {
		t.Error("banner shown without a session")
	}
}

func TestShelfWithSessionShowsTheBanner(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	addToken(t, st, lib, "TOK1")
	// A non-UTC clock on purpose. The store round-trips expires_at through
	// RFC3339 in UTC, so the banner formats a UTC value against a local
	// `now`; pinning UTC on both sides of that comparison is exactly how a
	// timezone-skewed deadline passed its tests.
	now := time.Date(2026, time.September, 3, 16, 12, 0, 0, chicago)

	sid, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sess := boulevard.Session{
		ID: sid, LibraryID: lib.ID, TokenID: "TOK1",
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/b/fairview/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "You're at the shelf.") {
		t.Error("banner missing for a live session")
	}
	if !strings.Contains(body, "4:12 PM tomorrow") {
		t.Errorf("deadline missing or wrong; body: %s", body)
	}
}

func TestShelfIgnoresASessionForAnotherLibrary(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	other := addLibrary(t, st, "whittier")
	addToken(t, st, other, "TOK1")
	now := time.Date(2026, time.September, 3, 16, 12, 0, 0, time.UTC)

	sid, _ := boulevard.NewSessionID(rand.Reader)
	if err := st.CreateSession(context.Background(), boulevard.Session{
		ID: sid, LibraryID: other.ID, TokenID: "TOK1",
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/b/fairview/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: string(sid)})
	rec := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "You're at the shelf.") {
		t.Error("a session for another library must not grant presence here")
	}
}

// TestPagesAreNotCacheable guards the one thing a shared cache in front of
// this server could get catastrophically wrong: the shelf's body depends on
// bl_session, so a stored copy hands one visitor's "You're at the shelf"
// banner to a stranger, or hands the person standing at the shelf a cached
// anonymous page.
func TestPagesAreNotCacheable(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	h := New(st, func() time.Time {
		return time.Date(2026, time.September, 15, 16, 12, 0, 0, chicago)
	}).Handler()

	for _, path := range []string{"/b/fairview/", "/b/fairview/about", "/b/nope/", "/s/" + tok.Secret, "/s/NOSUCHSECRET"} {
		rec := get(t, h, path)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", path, got)
		}
		if got := rec.Header().Get("Vary"); got != "Cookie" {
			t.Errorf("%s Vary = %q, want Cookie", path, got)
		}
	}
}

func TestUnknownSlugIs404(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()
	for _, path := range []string{"/b/nope/", "/b/nope/about"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404 — never fall back to the sole library", path, rec.Code)
		}
		// The designed page, not the stdlib's plain-text 404: this is the
		// one 404 a stale URL actually reaches, and it is read outdoors.
		body := rec.Body.String()
		if !strings.Contains(body, "That code isn't valid.") {
			t.Errorf("%s did not render the designed 404 page:\n%s", path, body)
		}
		// It must still name nothing, which is what makes it safe here.
		for _, forbidden := range []string{"The Fairview Boulevard", "/b/fairview/"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s leaked %q", path, forbidden)
			}
		}
	}
}

func TestAboutRenders(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := get(t, New(st, time.Now).Handler(), "/b/fairview/about")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// html/template escapes "&" to "&amp;" in text nodes (a correct security
	// property of the given about.html, which renders {{.Location}} as
	// plain text). The brief's literal "4th & Fairview, Minneapolis" can
	// never appear verbatim in the rendered bytes, so the check here uses
	// the escaped form — see task-6-report.md.
	for _, want := range []string{"The Fairview Boulevard", "4th &amp; Fairview, Minneapolis", "Presence is the only credential"} {
		if !strings.Contains(body, want) {
			t.Errorf("about page missing %q", want)
		}
	}
}
