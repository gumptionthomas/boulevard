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
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/b/"+lib.Slug+"/steward/queue") {
		t.Errorf("Location = %q, want it back on the queue", loc)
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
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/b/"+lib.Slug+"/steward/shelf") {
		t.Errorf("Location = %q, want it back on the shelf page", loc)
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
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/b/"+lib.Slug+"/steward/shed") {
		t.Errorf("Location = %q, want it back on the shed page", loc)
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

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got.State)
	}
}

// TestStewardThreePagesRenderTheirItems is a direct GET at each of the
// three list pages with real inventory (stewardServerWithItems) in every
// state — pending, shelved, shed. It exists mainly to exercise the display
// formatting for LeftAt, ShelvedAt and ShedAt without panicking: all three
// are stored-UTC timestamps converted via `$.Now.Location` in the template
// (steward-queue/shelf/shed.html), and the pin/approve tests above only
// exercise that path for pending and shelved items, never shed ones.
func TestStewardThreePagesRenderTheirItems(t *testing.T) {
	_, lib, key, h := stewardServerWithItems(t)
	c := loginAsSteward(t, h, lib, key)

	queue := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/queue", c)
	if queue.Code != http.StatusOK {
		t.Fatalf("queue status = %d, want 200", queue.Code)
	}
	if !strings.Contains(queue.Body.String(), "waiting one") {
		t.Errorf("queue page does not show its pending item:\n%s", queue.Body.String())
	}

	shelf := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/shelf", c)
	if shelf.Code != http.StatusOK {
		t.Fatalf("shelf status = %d, want 200", shelf.Code)
	}
	if !strings.Contains(shelf.Body.String(), "shelved") {
		t.Errorf("shelf page does not show its shelved item:\n%s", shelf.Body.String())
	}

	shed := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/shed", c)
	if shed.Code != http.StatusOK {
		t.Fatalf("shed status = %d, want 200", shed.Code)
	}
	if !strings.Contains(shed.Body.String(), "you took it down") {
		t.Errorf("shed page does not show the removed item's reason:\n%s", shed.Body.String())
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
