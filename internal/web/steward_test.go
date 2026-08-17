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

// stewardServer builds a store, adds a library, generates a steward key,
// stores its hash, and returns the plaintext key — the one and only time it
// exists in that form, exactly as `boulevard steward-key` would produce it.
func stewardServer(t *testing.T) (*store.Store, boulevard.Library, string) {
	t.Helper()
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")

	key, err := boulevard.NewStewardKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetStewardKeyHash(context.Background(), lib.ID, boulevard.HashStewardKey(key)); err != nil {
		t.Fatal(err)
	}
	return st, lib, key
}

// cookieNamed finds a cookie the handler set on the response, or nil.
func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// postForm submits a POST with a urlencoded body, optionally carrying a
// cookie — modeled on postLeave's request construction, but not an
// extension of it: postLeave is leave-specific (it mints its own presence
// session via withSession), where every steward test instead supplies
// whatever cookie its own login step already produced.
func postForm(t *testing.T, h http.Handler, path string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// getWithCookie is get, plus a cookie — for a steward route that needs the
// session cookie on a GET.
func getWithCookie(t *testing.T, h http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Every steward route 404s until a key exists. Not 403: a fresh install
// should not advertise a surface that is not armed.
func TestStewardRoutes404BeforeAKeyIsSet(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()

	for _, path := range []string{
		"/b/fairview/steward/", "/b/fairview/steward/login",
		"/b/fairview/steward/queue", "/b/fairview/steward/shelf",
		"/b/fairview/steward/shed", "/b/fairview/steward/settings",
	} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 before a key is set", path, rec.Code)
		}
	}
}

func TestStewardLoginWithTheRightKeySetsASession(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if cookieNamed(rec, "bl_steward") == nil {
		t.Fatal("no bl_steward cookie was set")
	}
}

func TestStewardLoginWithTheWrongKeyIsRefused(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {"WRONGWRONGWRONGWRONGWRONGW"}}, nil)
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a wrong key logged in")
	}
	if cookieNamed(rec, "bl_steward") != nil {
		t.Error("a cookie was set for a failed login")
	}
	if strings.Contains(rec.Body.String(), "WRONGWRONG") {
		t.Error("the submitted key was echoed back into the page")
	}
}

func TestStewardHubRequiresASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := get(t, h, "/b/"+lib.Slug+"/steward/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want a 303 to the login form", rec.Code)
	}
}

func TestStewardCookieIsHttpOnlyAndStrict(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	c := cookieNamed(rec, "bl_steward")
	if c == nil {
		t.Fatal("no cookie")
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("cookie is not SameSite=Strict — every steward route mutates")
	}
}

func TestStewardLogoutEndsTheSession(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	login := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	c := cookieNamed(login, "bl_steward")

	postForm(t, h, "/b/"+lib.Slug+"/steward/logout", url.Values{}, c)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	if rec.Code != http.StatusSeeOther {
		t.Error("the hub still rendered after logout")
	}
}

// A live session lets the hub through — it must not redirect once logged
// in. This is the positive case the brief's redirect tests do not exercise
// on their own: without it, a bug that redirected unconditionally would
// pass every test above.
func TestStewardHubRendersWithALiveSession(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	login := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	c := cookieNamed(login, "bl_steward")

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with a live session", rec.Code)
	}
}
