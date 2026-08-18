package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// The trap. UpdateLibrary writes nine columns from a whole Library value, so
// a handler that builds one from form input alone silently blanks the slug
// and the base URL — and would pass any test that checked only the six
// fields it meant to change.
func TestSettingsSaveDoesNotBlankTheSlugOrBaseURL(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	postForm(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name":     {"A New Name"},
		"location": {"Somewhere else"},
		"slots":    {"7"},
		"max_age":  {"45"},
		"copies":   {"2"},
	}, c)

	got, err := st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "A New Name" {
		t.Errorf("name = %q, want the new one", got.Name)
	}
	if got.Slug != lib.Slug {
		t.Errorf("slug = %q, want %q — a rename must not move the URL", got.Slug, lib.Slug)
	}
	if got.BaseURL != lib.BaseURL {
		t.Errorf("base URL = %q, want %q", got.BaseURL, lib.BaseURL)
	}
}

// The steward key hash is not on the Library struct precisely so this cannot
// break — assert it anyway, because it is the failure that locks a steward
// out of their own box.
func TestSettingsSaveDoesNotBreakLogin(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	postForm(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name": {"A New Name"}, "location": {"x"},
		"slots": {"7"}, "max_age": {"45"}, "copies": {"2"},
	}, c)

	ok, err := st.VerifyStewardKey(context.Background(), lib.ID, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("the steward key stopped working after a settings save")
	}
}

// Also unlisted among the six settable fields, and for the same reason as
// the slug and base URL: nothing on this form writes it, so a save must not
// disturb it either. Nothing currently sets it to a non-empty value (§6),
// but the read-modify-write must round-trip it regardless of what it holds.
func TestSettingsSaveDoesNotTouchStewardContact(t *testing.T) {
	st, lib, key := stewardServer(t)
	ctx := context.Background()

	seeded, err := st.LibraryByID(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	seeded.StewardContact = "steward@example.org"
	if err := st.UpdateLibrary(ctx, seeded); err != nil {
		t.Fatal(err)
	}

	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	postForm(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name": {"A New Name"}, "location": {"x"},
		"slots": {"7"}, "max_age": {"45"}, "copies": {"2"},
	}, c)

	got, err := st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.StewardContact != "steward@example.org" {
		t.Errorf("steward_contact = %q, want it untouched by a settings save", got.StewardContact)
	}
}

// A successful save must not carry its confirmation as free text in a
// redirect's query string: html/template escapes it, so it is not script
// injection, but it would still be prose rendered at the app's own
// authenticated URL, in the app's own voice — exactly what a phished
// "confirm your steward key" link would want to look like. The settings
// save is idempotent, so there is no double-submit hazard a 303 needs to
// guard against here; it re-renders in place instead.
func TestSettingsSaveDoesNotRedirectThroughANoticeParam(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name": {lib.Name}, "location": {lib.LocationLabel},
		"slots": {"12"}, "max_age": {"90"}, "copies": {"3"},
	}, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("a successful save redirected to %q instead of rendering in place", loc)
	}
}

// Final review, F8: every other test in this file omits the `approval` key
// entirely, and an unchecked HTML checkbox is simply not submitted — so the
// suite had never once observed ApprovalRequired surviving a save as true.
// It was driven to false on every save the tests performed, and it is the
// one setting that decides whether the approval queue exists at all. A case
// each way: checked survives as true, and a follow-up save with the field
// omitted (an unchecked box, as a browser would actually submit it) drives
// it back to false.
func TestSettingsSaveApprovalRequired(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	base := url.Values{
		"name": {"Fairview"}, "location": {"4th & Fairview"},
		"slots": {"12"}, "max_age": {"90"}, "copies": {"3"},
	}

	checked := url.Values{}
	for k, v := range base {
		checked[k] = v
	}
	checked.Set("approval", "on")
	postForm(t, h, "/b/"+lib.Slug+"/steward/settings", checked, c)

	got, err := st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !got.ApprovalRequired {
		t.Error("ApprovalRequired = false, want true after saving with the box checked")
	}

	unchecked := url.Values{}
	for k, v := range base {
		unchecked[k] = v
	}
	// approval intentionally omitted — an unchecked checkbox is not
	// submitted at all.
	postForm(t, h, "/b/"+lib.Slug+"/steward/settings", unchecked, c)

	got, err = st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ApprovalRequired {
		t.Error("ApprovalRequired = true, want false after saving with the box unchecked")
	}
}

// slots = 0, negative max age, negative copies, blank name: each re-renders
// the form with an error and changes nothing.
func TestSettingsRejectBadNumbers(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	before, err := st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read library: %v", err)
	}

	valid := url.Values{
		"name": {"Fairview"}, "location": {"4th & Fairview"},
		"slots": {"12"}, "max_age": {"90"}, "copies": {"3"},
	}
	cases := []struct {
		name  string
		field string
		value string
	}{
		{"zero slots", "slots", "0"},
		{"negative max age", "max_age", "-1"},
		{"negative copies", "copies", "-1"},
		{"blank name", "name", "   "},
		{"blank-after-trim location", "location", "   "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			for k, v := range valid {
				form[k] = v
			}
			form.Set(tc.field, tc.value)

			rec := postForm(t, h, "/b/"+lib.Slug+"/steward/settings", form, c)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `class="err"`) {
				t.Errorf("no field error rendered:\n%s", rec.Body.String())
			}

			got, err := st.LibraryByID(context.Background(), lib.ID)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got != before {
				t.Errorf("a rejected submission changed the library: got %+v, want unchanged %+v", got, before)
			}
		})
	}
}

// Nine shelved, set slots to 5: all nine still shelved afterwards, and the
// next approval evicts exactly one.
func TestLoweringSlotsShedsNothingImmediately(t *testing.T) {
	ctx := context.Background()
	st, lib, key := stewardServer(t)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

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

	for i := 0; i < 9; i++ {
		it := newItem(fmt.Sprintf("item %d", i))
		if err := st.CreateItem(ctx, lib.ID, it); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApproveItem(ctx, lib.ID, it.ID, now); err != nil {
			t.Fatal(err)
		}
	}

	tenth := newItem("tenth, still waiting")
	if err := st.CreateItem(ctx, lib.ID, tenth); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name": {lib.Name}, "location": {lib.LocationLabel},
		"slots": {"5"}, "max_age": {"90"}, "copies": {"3"},
	}, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a save re-renders in place rather than redirecting through a query-string notice", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "The shelf holds 9 items and you are setting 5 slots") {
		t.Errorf("save did not explain what will happen to the over-capacity shelf:\n%s", rec.Body.String())
	}

	shelved, err := st.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelved) != 9 {
		t.Fatalf("shelved = %d, want 9 — lowering slots must not evict anything immediately", len(shelved))
	}

	got, err := st.LibraryByID(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slots != 5 {
		t.Fatalf("slots = %d, want 5 — the setting itself must still take effect", got.Slots)
	}

	// The shelf now sits over its new capacity (9 shelved, 5 slots). The
	// next approval is where DESIGN.md's FIFO eviction actually fires.
	if _, err := st.ApproveItem(ctx, lib.ID, tenth.ID, now); err != nil {
		t.Fatalf("approve the tenth item: %v", err)
	}

	shelved, err = st.ShelvedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shelved) != 9 {
		t.Errorf("shelved after the next approval = %d, want 9 (one evicted, one added)", len(shelved))
	}

	shed, err := st.ShedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shed) != 1 {
		t.Errorf("shed = %d, want exactly 1 — the next approval evicts exactly one", len(shed))
	}
}
