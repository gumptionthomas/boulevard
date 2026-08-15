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
	if c.Secure {
		t.Error("Secure must not be set on a plain-HTTP request")
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

func TestScanSetsSecureCookieOverTLS(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	tok := september(t, st, lib, boulevard.TokenPending)
	now := time.Date(2026, time.September, 15, 16, 12, 0, 0, time.UTC)

	req := httptest.NewRequest(http.MethodGet, "/s/"+tok.Secret, nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	New(st, func() time.Time { return now }).Handler().ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && !c.Secure {
			t.Error("Secure must be set when the request arrived over TLS")
		}
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
