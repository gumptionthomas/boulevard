package web

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// seedToken installs one token with an explicit window.
func seedToken(t *testing.T, st *store.Store, lib boulevard.Library, idx int, from, until boulevard.Date, state boulevard.TokenState) boulevard.Token {
	t.Helper()
	secret, err := tokens.NewSecret(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokID, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	tok := boulevard.Token{
		ID: tokID, LibraryID: lib.ID, Secret: secret, PeriodIndex: idx,
		ValidFrom: from, ValidUntil: until, State: state,
	}
	if err := st.InsertTokens(context.Background(), lib.ID, []boulevard.Token{tok}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func september(t *testing.T, st *store.Store, lib boulevard.Library, state boulevard.TokenState) boulevard.Token {
	t.Helper()
	return seedToken(t, st, lib, 2,
		boulevard.NewDate(2026, time.September, 1),
		boulevard.NewDate(2026, time.September, 30), state)
}

// addLibraryWithBaseURL is addLibrary with an explicit BaseURL, for the F12
// cookie-derivation test above — addLibrary itself always uses
// https://example.org, and this needs an http:// library too.
func addLibraryWithBaseURL(t *testing.T, st *store.Store, slug, baseURL string) boulevard.Library {
	t.Helper()
	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lib := boulevard.Library{
		ID: id, Slug: slug, Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       baseURL,
	}
	if err := st.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	return lib
}

func scanAt(t *testing.T, st *store.Store, secret string, now time.Time) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s/"+secret, nil)
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)
	return rec
}

func TestScanGrantedRedirectsAndSetsCookie(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, time.UTC)

	rec := scanAt(t, st, tok.Secret, now)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/b/fairview/" {
		t.Errorf("Location = %q, want /b/fairview/", got)
	}

	var c *http.Cookie
	for _, got := range rec.Result().Cookies() {
		if got.Name == cookieName {
			c = got
		}
	}
	if c == nil {
		t.Fatalf("no %s cookie set", cookieName)
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.MaxAge != 86400 {
		t.Errorf("MaxAge = %d, want 86400", c.MaxAge)
	}
	// addLibrary's BaseURL is https://example.org, and Secure is derived
	// from that (F12) rather than from r.TLS on this request, which
	// httptest.NewRequest leaves nil — mirroring the real deployment
	// DESIGN.md §8 recommends, where serve terminates no TLS itself and an
	// https:// install always sits behind a reverse proxy that does.
	if !c.Secure {
		t.Error("Secure must be set for an https:// library, even though this request itself has no TLS")
	}
}

func TestScanGrantedPersistsASessionNamingItsToken(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, time.UTC)

	rec := scanAt(t, st, tok.Secret, now)
	var value string
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			value = c.Value
		}
	}
	sess, err := st.SessionByID(context.Background(), boulevard.SessionID(value), now)
	if err != nil {
		t.Fatalf("session was not persisted: %v", err)
	}
	if sess.TokenID != tok.ID {
		t.Errorf("TokenID = %q, want %q", sess.TokenID, tok.ID)
	}
	if sess.LibraryID != lib.ID {
		t.Errorf("LibraryID = %q, want %q", sess.LibraryID, lib.ID)
	}
}

// Final review, F12: Secure used to be r.TLS != nil, which is never true in
// the deployment DESIGN.md §8 recommends — serve terminates no TLS itself,
// so an https:// install always sits behind a reverse proxy, and r.TLS on
// the request this process actually receives is always nil regardless. This
// pins the fix in both directions, each with r.TLS set to the opposite of
// what the old, buggy derivation would have wanted, so it fails immediately
// if Secure is ever wired back to r.TLS: an https:// library must get
// Secure with r.TLS nil, and an http:// library must not get Secure even
// with r.TLS set.
func TestScanCookieSecureFollowsLibraryBaseURLNotRequestTLS(t *testing.T) {
	st := testStore(t)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, time.UTC)

	httpsLib := addLibrary(t, st, "fairview") // BaseURL: https://example.org
	httpsTok := september(t, st, httpsLib, boulevard.TokenPending)
	req := httptest.NewRequest(http.MethodGet, "/s/"+httpsTok.Secret, nil)
	// r.TLS left nil on purpose.
	rec := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)
	c := cookieNamed(rec, cookieName)
	if c == nil {
		t.Fatal("no cookie set for the https:// library")
	}
	if !c.Secure {
		t.Error("Secure must be set for an https:// library, even when r.TLS is nil")
	}

	httpLib := addLibraryWithBaseURL(t, st, "riverside", "http://example.org")
	httpTok := september(t, st, httpLib, boulevard.TokenPending)
	req2 := httptest.NewRequest(http.MethodGet, "/s/"+httpTok.Secret, nil)
	req2.TLS = &tls.ConnectionState{} // set on purpose.
	rec2 := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec2, req2)
	c2 := cookieNamed(rec2, cookieName)
	if c2 == nil {
		t.Fatal("no cookie set for the http:// library")
	}
	if c2.Secure {
		t.Error("Secure must not be set for an http:// library, even when r.TLS is non-nil")
	}
}

func TestScanGrantedActivatesTheToken(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, time.UTC)

	scanAt(t, st, tok.Secret, now)

	all, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].State != boulevard.TokenActive {
		t.Errorf("state = %q, want active", all[0].State)
	}
	if all[0].FirstSeenAt == nil {
		t.Error("FirstSeenAt was not stamped")
	}
}

func TestScanOutOfWindowRendersTheDiagnostic(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	// 8 October: one day past the seven-day grace.
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

	rec := scanAt(t, st, tok.Secret, now)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — this is diagnostic, not a failure", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"This card is out of date.",
		"The steward needs to swap in the next card.",
		"The Fairview Boulevard",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("no session may be minted for an out-of-window card")
	}
	if strings.Contains(body, "isn't in use yet") {
		t.Error("an expired card must not render the not-yet copy")
	}
}

// TestScanBeforeItsWindowSaysSoRatherThanOutOfDate covers the other side of
// the window. A single OutOfWindow outcome served the expired copy for both,
// so card 12 of a fresh booklet was told it was "out of date" and had
// "stopped working" on a date that has not happened yet.
func TestScanBeforeItsWindowSaysSoRatherThanOutOfDate(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	// Card 12 of a booklet printed in August 2026: July 2027.
	tok := seedToken(t, st, lib, 12,
		boulevard.NewDate(2027, time.July, 1),
		boulevard.NewDate(2027, time.July, 31), boulevard.TokenPending)
	now := time.Date(2026, time.August, 14, 20, 25, 0, 0, chicago)

	rec := scanAt(t, st, tok.Secret, now)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — this is diagnostic, not a failure", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"This card isn't in use yet.",
		"July 2027",
		// Seven days of grace before the window, and the year, because
		// "24 June" alone reads as a date that has already passed.
		"It starts working on 24 June 2027",
		"The Fairview Boulevard",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"out of date", "stopped working"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("a card that has not started must not be described with %q", forbidden)
		}
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("no session may be minted for a card that is not in use yet")
	}

	all, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].State != boulevard.TokenPending || all[0].FirstSeenAt != nil {
		t.Errorf("a too-early scan must not activate or stamp the card: state=%q first_seen=%v",
			all[0].State, all[0].FirstSeenAt)
	}
}

func TestScanUnknownSecretNamesNothing(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	rec := scanAt(t, st, "QQQQQQQQQQQQQQQQQQQQQQQQQQ", time.Now())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "That code isn't valid.") {
		t.Error("body missing the invalid message")
	}
	// Spec §4.2: an unknown secret resolves to no library at all.
	if strings.Contains(body, "The Fairview Boulevard") {
		t.Error("the invalid page must not name a library")
	}
	if strings.Contains(body, "/b/fairview/") {
		t.Error("the invalid page must not link to a shelf")
	}
}

func TestScanRevokedIsIndistinguishableFromUnknown(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenRevoked)
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)

	revoked := scanAt(t, st, tok.Secret, now)
	unknown := scanAt(t, st, "QQQQQQQQQQQQQQQQQQQQQQQQQQ", now)

	if revoked.Code != unknown.Code {
		t.Errorf("status %d vs %d — revoked and unknown must be indistinguishable", revoked.Code, unknown.Code)
	}
	if revoked.Body.String() != unknown.Body.String() {
		t.Error("revoked and unknown must render byte-identical pages, or the response confirms the secret was real")
	}
	if len(revoked.Result().Cookies()) != 0 {
		t.Error("a revoked card must mint no session")
	}
}
