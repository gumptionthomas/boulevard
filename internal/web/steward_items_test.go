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
	"github.com/gumptionthomas/boulevard/internal/store"
)

// leaveItem seeds one pending item directly through the store, the same
// fixture style stewardServerWithItems already uses — these tests have no
// need to drive the leave/approve HTTP routes to get an item into a given
// state.
func leaveItem(t *testing.T, st *store.Store, lib boulevard.Library, note string, at time.Time) boulevard.Item {
	t.Helper()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemText, Payload: "x",
		Note: note, State: boulevard.ItemPending, LeftAt: at,
	}
	if err := st.CreateItem(context.Background(), lib.ID, it); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestStewardApproveMovesAPendingItemToTheShelf(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/approve", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := stewardPath(lib) + "queue?ok=shelved"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %q, want shelved", got.State)
	}
}

func TestStewardRejectReleasesAPendingItem(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/reject", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := stewardPath(lib) + "queue?ok=rejected"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got.State)
	}
}

func TestStewardRemoveShedsAShelvedItemWithReasonRemoved(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/remove", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := stewardPath(lib) + "shelf?ok=removed"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %q, want shed", got.State)
	}
	if got.ShedReason != boulevard.ShedRemoved {
		t.Errorf("shed_reason = %q, want %q", got.ShedReason, boulevard.ShedRemoved)
	}
}

func TestStewardPinThenUnpinRoundTrips(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/pin", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("pin status = %d, want 303", rec.Code)
	}
	wantPin := stewardPath(lib) + "shelf?ok=pinned"
	if loc := rec.Header().Get("Location"); loc != wantPin {
		t.Errorf("pin Location = %q, want %q", loc, wantPin)
	}
	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned {
		t.Fatal("item is not pinned after pin")
	}

	rec = postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/unpin", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unpin status = %d, want 303", rec.Code)
	}
	wantUnpin := stewardPath(lib) + "shelf?ok=unpinned"
	if loc := rec.Header().Get("Location"); loc != wantUnpin {
		t.Errorf("unpin Location = %q, want %q", loc, wantUnpin)
	}
	got, err = st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pinned {
		t.Fatal("item is still pinned after unpin")
	}
}

// TestStewardFourthPinNamesTheThreePinned pins three items, then tries a
// fourth. DESIGN.md §5 caps pins at 3; a count alone does not tell the
// steward which items are blocking, so the refusal must name them by note
// rather than just say "pin limit reached".
func TestStewardFourthPinNamesTheThreePinned(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	var pinnedNotes []string
	for i, note := range []string{"first pin", "second pin", "third pin"} {
		it := leaveItem(t, st, lib, note, now.Add(time.Duration(i)*time.Minute))
		if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, now); err != nil {
			t.Fatal(err)
		}
		rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/pin", nil, c)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("pin %d status = %d, want 303", i, rec.Code)
		}
		pinnedNotes = append(pinnedNotes, note)
	}

	fourth := leaveItem(t, st, lib, "the fourth", now.Add(10*time.Minute))
	if _, err := st.ApproveItem(context.Background(), lib.ID, fourth.ID, now); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+fourth.ID+"/pin", nil, c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	for _, note := range pinnedNotes {
		if !strings.Contains(body, note) {
			t.Errorf("refusal does not name pinned item %q:\n%s", note, body)
		}
	}

	got, err := st.ItemByID(context.Background(), lib.ID, fourth.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pinned {
		t.Error("the fourth item was pinned despite the limit")
	}
}

// TestStewardApproveOntoAnAllPinnedShelfRendersTheRefusal is the one test
// that matters most in this task: ApproveItem's ErrAllPinned branch
// (store/item.go) was unreachable before PinItem existed to set `pinned`,
// and a handler that forgets it turns a legitimate steward action into a
// 500 instead of a page naming what to do. Modeled on the CLI's own
// TestApproveOntoAnAllPinnedShelfNamesThePins (cmd/boulevard/queue_cli_test.go).
func TestStewardApproveOntoAnAllPinnedShelfRendersTheRefusal(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	// Shrink the shelf to one slot, fill it, and pin the only occupant so
	// nothing is evictable.
	pinned := leaveItem(t, st, lib, "the only pin", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, pinned.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.PinItem(context.Background(), lib.ID, pinned.ID, 3); err != nil {
		t.Fatal(err)
	}
	stored, err := st.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.Slots = 1
	if err := st.UpdateLibrary(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	waiting := leaveItem(t, st, lib, "still waiting", now.Add(time.Hour))

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+waiting.ID+"/approve", nil, c)
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("approve onto an all-pinned shelf 500ed instead of rendering the refusal:\n%s", rec.Body.String())
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "the only pin") {
		t.Errorf("refusal does not name the pinned item blocking it:\n%s", body)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemPending {
		t.Errorf("state = %q, want the refused item to stay pending", got.State)
	}
	pin, err := st.ItemByID(context.Background(), lib.ID, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pin.State != boulevard.ItemShelved {
		t.Errorf("pinned item state = %q, want it to stay shelved, unevicted", pin.State)
	}
}

func TestStewardReshelveReturnsAShedItemToTheShelf(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/reshelve", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := stewardPath(lib) + "shed?ok=reshelved"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %q, want shelved", got.State)
	}
}

func TestStewardReleaseEndsAShedItem(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	it := leaveItem(t, st, lib, "the item", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+it.ID+"/release", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := stewardPath(lib) + "shed?ok=released"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got.State)
	}
}

// TestStewardThreePagesRenderTheirItemsInTheInjectedClocksZone is a direct
// GET at each of the three list pages with real inventory in every state —
// pending, shelved, shed — on a non-UTC injected clock (`chicago`,
// render_test.go's fixed UTC-5 zone), asserting the rendered timestamp
// itself rather than just that the page did not panic.
//
// Review flagged the first version of this test: every clock in this file
// was `time.UTC`, so `.In $.Now.Location` was a no-op in every case, and
// nothing asserted a rendered string at all — `TZ=America/Chicago go test`
// does not catch that either, since it moves time.Local, which the
// templates deliberately never touch (they read `$.Now.Location`, the
// injected clock, per CLAUDE.md's rule against reading a clock in place).
//
// The seeded instant, 03:00 UTC, is chosen to land on the *previous*
// calendar day once shifted five hours back into Chicago — the case that
// fails if the conversion is ever swapped for `.Local` (still UTC-backed in
// this process, so it would show the UTC day) or dropped outright (same
// failure, plus the wrong clock time).
func TestStewardThreePagesRenderTheirItemsInTheInjectedClocksZone(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 9, 0, 0, 0, chicago)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	prevDayUTC := time.Date(2026, time.August, 17, 3, 0, 0, 0, time.UTC)
	const wantChicago = "16 Aug, 10:00 PM" // 03:00 UTC minus five hours

	leaveItem(t, st, lib, "waiting one", prevDayUTC)

	shelved := leaveItem(t, st, lib, "shelved", prevDayUTC)
	if _, err := st.ApproveItem(context.Background(), lib.ID, shelved.ID, prevDayUTC); err != nil {
		t.Fatal(err)
	}

	shed := leaveItem(t, st, lib, "shed", prevDayUTC)
	if _, err := st.ApproveItem(context.Background(), lib.ID, shed.ID, prevDayUTC); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveItem(context.Background(), lib.ID, shed.ID, prevDayUTC); err != nil {
		t.Fatal(err)
	}

	queue := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/queue", c)
	if queue.Code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200", queue.Code)
	}
	if body := queue.Body.String(); !strings.Contains(body, "waiting one") || !strings.Contains(body, wantChicago) {
		t.Errorf("queue page does not show the pending item at %s:\n%s", wantChicago, body)
	}

	shelf := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/shelf", c)
	if shelf.Code != http.StatusOK {
		t.Fatalf("shelf status = %d, want 200", shelf.Code)
	}
	if body := shelf.Body.String(); !strings.Contains(body, "shelved") || !strings.Contains(body, wantChicago) {
		t.Errorf("shelf page does not show the shelved item at %s:\n%s", wantChicago, body)
	}

	shedPage := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/shed", c)
	if shedPage.Code != http.StatusOK {
		t.Fatalf("shed status = %d, want 200", shedPage.Code)
	}
	if body := shedPage.Body.String(); !strings.Contains(body, "you took it down") || !strings.Contains(body, wantChicago) {
		t.Errorf("shed page does not show the removed item at %s:\n%s", wantChicago, body)
	}
}

// TestStewardUnknownOKCodeRendersNothing is the Critical fix's own required
// test: a forged, stale, or mistyped `?ok=` code must render nothing at
// all, not echo the code and not fall back to some other page's
// confirmation.
func TestStewardUnknownOKCodeRendersNothing(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/queue?ok=totally-made-up", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "totally-made-up") {
		t.Error("an unknown ok code was echoed onto the page")
	}
	for code, msg := range okMessages {
		if strings.Contains(body, msg) {
			t.Errorf("an unknown ok code rendered %q's confirmation (%q)", code, msg)
		}
	}
}

// TestStewardApproveOntoAFullEvictableShelfNamesTheEviction is the notice
// mechanism's one genuinely dynamic case: ApproveItem's `evicted` return
// value decides between the "shelved" and "shelved-evicted" codes. Every
// other approve test in this file either has room to spare or has nothing
// evictable (all-pinned), so this is the first to actually evict.
func TestStewardApproveOntoAFullEvictableShelfNamesTheEviction(t *testing.T) {
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	oldest := leaveItem(t, st, lib, "the oldest", now)
	if _, err := st.ApproveItem(context.Background(), lib.ID, oldest.ID, now); err != nil {
		t.Fatal(err)
	}
	stored, err := st.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.Slots = 1
	if err := st.UpdateLibrary(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	waiting := leaveItem(t, st, lib, "still waiting", now.Add(time.Hour))

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/i/"+waiting.ID+"/approve", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	wantLoc := stewardPath(lib) + "queue?ok=shelved-evicted"
	gotLoc := rec.Header().Get("Location")
	if gotLoc != wantLoc {
		t.Errorf("Location = %q, want %q", gotLoc, wantLoc)
	}

	follow := getWithCookie(t, h, gotLoc, c)
	if follow.Code != http.StatusOK {
		t.Fatalf("following the redirect: status = %d, want 200", follow.Code)
	}
	if !strings.Contains(follow.Body.String(), okMessages["shelved-evicted"]) {
		t.Errorf("queue page does not show the eviction confirmation after following the redirect:\n%s", follow.Body.String())
	}

	got, err := st.ItemByID(context.Background(), lib.ID, oldest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemShed || got.ShedReason != boulevard.ShedEvicted {
		t.Errorf("evicted item state/reason = %q/%q, want shed/evicted", got.State, got.ShedReason)
	}
}

// TestStewardSevenMutationsOnAnUnknownIDAre404 pins the ErrNotFound branch
// the brief singles out and all seven POST handlers implement identically.
// TestAllTenStewardItemRoutesRequireASession never reaches a store call at
// all (it has no session and stops at the login redirect); this is the
// first test that actually posts, as a logged-in steward, an id naming
// nothing.
func TestStewardSevenMutationsOnAnUnknownIDAre404(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)
	fakeID := "AAAAAAAAAAAAAAAAAAAAAAAAAA"

	for _, action := range []string{"approve", "reject", "remove", "pin", "unpin", "reshelve", "release"} {
		path := "/b/" + lib.Slug + "/steward/i/" + fakeID + "/" + action
		rec := postForm(t, h, path, nil, c)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s on an unknown id = %d, want 404", action, rec.Code)
		}
	}
}

// TestAllTenStewardItemRoutesRequireASession pins the auth gate across every
// route this task adds: a handler that forgot to start with requireSteward
// would render its body (or worse, mutate) for an anonymous caller instead
// of redirecting to the login form, the way TestStewardHubRequiresASession
// already pins for the hub.
func TestAllTenStewardItemRoutesRequireASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()
	fakeID := "AAAAAAAAAAAAAAAAAAAAAAAAAA"

	type route struct {
		method, path string
	}
	base := "/b/" + lib.Slug + "/steward/"
	routes := []route{
		{http.MethodGet, base + "queue"},
		{http.MethodGet, base + "shelf"},
		{http.MethodGet, base + "shed"},
		{http.MethodPost, base + "i/" + fakeID + "/approve"},
		{http.MethodPost, base + "i/" + fakeID + "/reject"},
		{http.MethodPost, base + "i/" + fakeID + "/remove"},
		{http.MethodPost, base + "i/" + fakeID + "/pin"},
		{http.MethodPost, base + "i/" + fakeID + "/unpin"},
		{http.MethodPost, base + "i/" + fakeID + "/reshelve"},
		{http.MethodPost, base + "i/" + fakeID + "/release"},
	}

	for _, rt := range routes {
		var rec *httptest.ResponseRecorder
		if rt.method == http.MethodGet {
			rec = get(t, h, rt.path)
		} else {
			rec = postForm(t, h, rt.path, nil, nil)
		}
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s %s = %d, want 303 to login without a session", rt.method, rt.path, rec.Code)
			continue
		}
		want := stewardPath(lib) + "login"
		if loc := rec.Header().Get("Location"); loc != want {
			t.Errorf("%s %s Location = %q, want %q", rt.method, rt.path, loc, want)
		}
	}
}
