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
	return st, lib, armWithStewardKey(t, st, lib)
}

// armWithStewardKey generates a steward key for an already-created library
// and stores its hash, returning the plaintext key. Factored out of
// stewardServer so a test needing two armed libraries (the cross-library
// session test below) does not have to duplicate the key-generation
// boilerplate.
func armWithStewardKey(t *testing.T, st *store.Store, lib boulevard.Library) string {
	t.Helper()
	key, err := boulevard.NewStewardKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetStewardKeyHash(context.Background(), lib.ID, boulevard.HashStewardKey(key)); err != nil {
		t.Fatal(err)
	}
	return key
}

// stewardServerWithItems builds on stewardServer with a small shelf: two
// items waiting in the approval queue, one shelved, and one shed — enough
// for the hub's four rows to each show a real, distinguishable number.
// Items are seeded directly through the store (CreateItem, ApproveItem,
// RemoveItem), the same fixture style take_test.go's takeServer already
// established, rather than driving them through the leave/approve HTTP
// routes this test has no need to exercise.
func stewardServerWithItems(t *testing.T) (*store.Store, boulevard.Library, string, http.Handler) {
	t.Helper()
	ctx := context.Background()
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	newItem := func(note string) boulevard.Item {
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		return boulevard.Item{
			ID: id, LibraryID: lib.ID, Type: boulevard.ItemText, Payload: "x",
			Note: note, State: boulevard.ItemPending, LeftAt: now,
		}
	}

	for _, note := range []string{"waiting one", "waiting two"} {
		it := newItem(note)
		if err := st.CreateItem(ctx, lib.ID, it); err != nil {
			t.Fatal(err)
		}
	}

	shelved := newItem("shelved")
	if err := st.CreateItem(ctx, lib.ID, shelved); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, lib.ID, shelved.ID, now); err != nil {
		t.Fatal(err)
	}

	// Shelve, then take it down again, so ShedItems has one row shed for
	// "removed" rather than requiring eviction or expiry machinery here.
	shed := newItem("shed")
	if err := st.CreateItem(ctx, lib.ID, shed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveItem(ctx, lib.ID, shed.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveItem(ctx, lib.ID, shed.ID, now); err != nil {
		t.Fatal(err)
	}

	h := New(st, func() time.Time { return now }).Handler()
	return st, lib, key, h
}

// loginAsSteward posts the steward key and returns the session cookie the
// login set, failing the test if login did not succeed.
func loginAsSteward(t *testing.T, h http.Handler, lib boulevard.Library, key string) *http.Cookie {
	t.Helper()
	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login", url.Values{"key": {key}}, nil)
	c := cookieNamed(rec, "bl_steward")
	if c == nil {
		t.Fatal("login did not set a bl_steward cookie")
	}
	return c
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

// The 404-before-a-key gate is exercised by GET in the test above, but the
// login POST is the one unauthenticated write endpoint on this server (spec
// §2 names it explicitly) — it goes through the same stewardKeyed call, but
// that is worth pinning directly rather than by inference.
func TestStewardLoginPOSTWithNoKeySetIs404(t *testing.T) {
	st := testStore(t)
	lib := addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login", url.Values{"key": {"anything"}}, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 before a key is set", rec.Code)
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
	// addLibrary sets BaseURL to an https:// origin, so Secure must follow.
	if !c.Secure {
		t.Error("cookie is not Secure for an https:// library")
	}
	wantPath := "/b/" + lib.Slug + "/steward/"
	if c.Path != wantPath {
		t.Errorf("cookie Path = %q, want %q — expireStewardCookie clears the same path on logout", c.Path, wantPath)
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

func TestHubCountsWhatNeedsTheSteward(t *testing.T) {
	// two waiting, one shelved, one shed
	_, lib, key, h := stewardServerWithItems(t)
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	body := rec.Body.String()

	if !strings.Contains(body, "Waiting for you") {
		t.Error("no waiting row")
	}
	if !strings.Contains(body, "2") {
		t.Error("the waiting count is not shown")
	}
}

// The state a steward sees most. §6 calls the public empty state arguably
// the most important screen; this is its admin counterpart.
func TestHubSaysNothingNeedsYouWhenNothingDoes(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	if !strings.Contains(rec.Body.String(), "Nothing needs you") {
		t.Error("an idle box does not say so")
	}
}

// Beyond the brief's two given tests: the shelf row and shed row both carry
// numbers no other assertion here pins down, so a handler that swapped
// Shelved/Slots or dropped the reason tally would still pass the two tests
// above.
func TestHubShowsSlotsAndTheShedBreakdown(t *testing.T) {
	_, lib, key, h := stewardServerWithItems(t)
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	body := rec.Body.String()

	// stewardServerWithItems shelves one item on a fresh library, whose
	// Slots comes from migration 2's default (12) since CreateLibrary
	// never writes the settings columns itself.
	if !strings.Contains(body, "1 of 12 slots") {
		t.Errorf("shelf row does not show 1 of 12 slots:\n%s", body)
	}
	// RemoveItem sheds with reason "removed", whose Label() is "you took
	// it down" (boulevard.ShedReason.Label, internal/boulevard/item.go).
	if !strings.Contains(body, "you took it down") {
		t.Errorf("shed row does not break down the one removed item:\n%s", body)
	}
}

// stewardSession compares sess.LibraryID != lib.ID and fails closed, but
// nothing exercised it: in single-library mode nothing would notice if the
// line were deleted, which makes this the §10 rule this milestone is most
// likely to lose silently. Modeled on TestTakeWithAnotherLibrarysSessionIs403
// and TestLeaveSubmitWithAnotherLibrarysSessionIs403 (take_test.go,
// leave_test.go): a session minted at one library must grant nothing at
// another.
func TestStewardSessionDoesNotCrossLibraries(t *testing.T) {
	st := testStore(t)
	libA := addLibrary(t, st, "fairview")
	libB := addLibrary(t, st, "riverside")
	keyA := armWithStewardKey(t, st, libA)
	_ = armWithStewardKey(t, st, libB)

	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, libA, keyA)

	rec := getWithCookie(t, h, "/b/"+libB.Slug+"/steward/", c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want a 303 to B's login form — A's cookie must not open B's hub", rec.Code)
	}
	want := stewardPath(libB) + "login"
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// The renewal threshold is the write-cost guard spec §3 makes explicitly: a
// steward loading five admin pages should not cost five writes through the
// one connection SetMaxOpenConns(1) allows, so a session is renewed only
// once less than half its 30-day window remains. Nothing else in the suite
// would catch the comparison written backwards, or the branch deleted
// outright, in either direction — so both are pinned here, on an injected
// non-UTC clock per the project's rule for anything comparing times.
func TestStewardSessionRenewalThreshold(t *testing.T) {
	st, lib, key := stewardServer(t)

	loginAt := time.Date(2026, time.August, 1, 9, 0, 0, 0, chicago)
	var current time.Time
	h := New(st, func() time.Time { return current }).Handler()

	current = loginAt
	login := postForm(t, h, "/b/"+lib.Slug+"/steward/login", url.Values{"key": {key}}, nil)
	c := cookieNamed(login, "bl_steward")
	if c == nil {
		t.Fatal("login did not set a bl_steward cookie")
	}
	originalExpiry := loginAt.Add(boulevard.StewardSessionTTL)

	// One day in: 29 of 30 days remain, comfortably outside the renewal
	// window, so the request must leave expires_at untouched.
	current = loginAt.Add(24 * time.Hour)
	getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	sess, err := st.StewardSessionByID(context.Background(), c.Value, current)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ExpiresAt.Equal(originalExpiry) {
		t.Errorf("expires_at changed after a request one day in: got %v, want unchanged %v",
			sess.ExpiresAt, originalExpiry)
	}

	// Sixteen days in: 14 of 30 days remain, inside the renewal window, so
	// the request must push expires_at out a fresh 30 days from now.
	current = loginAt.Add(16 * 24 * time.Hour)
	getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	sess, err = st.StewardSessionByID(context.Background(), c.Value, current)
	if err != nil {
		t.Fatal(err)
	}
	wantRenewed := current.Add(boulevard.StewardSessionTTL)
	if !sess.ExpiresAt.Equal(wantRenewed) {
		t.Errorf("expires_at not renewed after a request sixteen days in: got %v, want %v",
			sess.ExpiresAt, wantRenewed)
	}
}
