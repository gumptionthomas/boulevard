package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

func TestTokensPage404sBeforeAKeyIsSet(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()
	if rec := get(t, h, "/b/fairview/steward/tokens"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestTokensPageRedirectsWithoutASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()
	rec := get(t, h, "/b/"+lib.Slug+"/steward/tokens")
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rec.Code)
	}
}

// The page must never print a secret: it is the shelf's write credential,
// and this page is the one a steward is most likely to screenshot.
func TestTokensPageNeverPrintsASecret(t *testing.T) {
	st, lib, key := stewardServer(t)
	toks := seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, tok := range toks {
		if strings.Contains(body, tok.Secret) {
			t.Fatal("a token secret was rendered into the tokens page")
		}
	}
	for _, want := range []string{"Card 1 of 12", "August"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestForceActivateFromTheDesk(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/3/force-activate", nil, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); !strings.HasSuffix(got, "/steward/tokens?ok=activated") {
		t.Errorf("Location = %q, want the tokens page with ok=activated", got)
	}

	toks, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		if tok.PeriodIndex == 3 && tok.State != boulevard.TokenActive {
			t.Errorf("period 3 state = %q, want active", tok.State)
		}
	}
}

func TestForceActivateRefusalNamesTheState(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/3/force-activate", nil, c)
	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/3/force-activate", nil, c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "active") {
		t.Errorf("the refusal does not name the state it found:\n%s", rec.Body.String())
	}
}

// Revoking must actually stop the card, and the failure page must be the
// generic one — §4 requires a revoked secret to be indistinguishable from
// an unknown one.
func TestRevokeFromTheDeskStopsTheCardScanning(t *testing.T) {
	st, lib, key := stewardServer(t)
	toks := seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	// A successful scan is a 302 to the shelf, not a 303 — see scan.go.
	if rec := get(t, h, "/s/"+toks[0].Secret); rec.Code != http.StatusFound {
		t.Fatalf("before revoke: scan status = %d, want 302", rec.Code)
	}
	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/revoke", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303", rec.Code)
	}

	revoked := get(t, h, "/s/"+toks[0].Secret)
	unknown := get(t, h, "/s/ZZZZZZZZZZZZZZZZZZZZZZZZZZ")
	if revoked.Code != unknown.Code || revoked.Body.String() != unknown.Body.String() {
		t.Error("a revoked secret is distinguishable from an unknown one")
	}
}

func TestRotateFromTheDeskKeepsTheActiveCard(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/force-activate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup force-activate: status = %d", rec.Code)
	}
	before, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var activeSecret string
	for _, tok := range before {
		if tok.State == boulevard.TokenActive {
			activeSecret = tok.Secret
		}
	}

	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/rotate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("rotate status = %d, want 303", rec.Code)
	}

	after, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != tokens.PeriodCount+1 {
		t.Fatalf("%d tokens after rotate, want %d", len(after), tokens.PeriodCount+1)
	}
	fresh := 0
	kept := false
	for _, tok := range after {
		if tok.Secret == activeSecret && tok.State == boulevard.TokenActive {
			kept = true
		}
		if tok.PeriodIndex >= 13 && tok.PeriodIndex <= 24 && tok.State == boulevard.TokenPending {
			fresh++
		}
	}
	if !kept {
		t.Error("the card in the door did not survive the rotation")
	}
	if fresh != tokens.PeriodCount {
		t.Errorf("%d fresh cards at 13..24, want %d", fresh, tokens.PeriodCount)
	}
}

// The desk's two irreversible operations must ask first. The CLI has always
// blocked on a prompt for both; the page offering them as a bare tap was
// the phone surface having the least friction for the acts that cannot be
// undone.
func TestTheDeskAsksBeforeRevokingOrRotating(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	body := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens", c).Body.String()
	for _, direct := range []string{
		`action="/b/` + lib.Slug + `/steward/tokens/1/revoke"`,
		`action="/b/` + lib.Slug + `/steward/tokens/rotate"`,
	} {
		if strings.Contains(body, direct) {
			t.Errorf("the tokens page still posts %s without confirming", direct)
		}
	}
	for _, want := range []string{
		"/steward/tokens/1/revoke/confirm",
		"/steward/tokens/rotate/confirm",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the tokens page does not link to %s", want)
		}
	}
}

// The rotate confirmation names the count it is about to discard, and says
// which card survives — the CLI's printRotateWarning, on the phone.
func TestRotateConfirmationNamesWhatItDiscards(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	// Nothing scanned yet: there is no card in the door, so all twelve
	// printed cards are discarded and the box cannot be written to until a
	// new booklet is carried to it. This is the case that reads as harmless
	// and costs the most.
	dark := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens/rotate/confirm", c)
	if dark.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", dark.Code)
	}
	for _, want := range []string{"12 cards are replaced", "nothing can be left or taken"} {
		if !strings.Contains(dark.Body.String(), want) {
			t.Errorf("the no-active-card confirmation does not say %q:\n%s", want, dark.Body.String())
		}
	}

	// With a card in the door, eleven go and one stays.
	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/force-activate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup force-activate: status = %d", rec.Code)
	}
	lit := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens/rotate/confirm", c)
	for _, want := range []string{"keeps working", "11 unprinted cards"} {
		if !strings.Contains(lit.Body.String(), want) {
			t.Errorf("the confirmation does not say %q:\n%s", want, lit.Body.String())
		}
	}
}

// Spec §3: revoking the active card stops anyone leaving or taking until
// someone walks to it with a different card, and the confirmation says so
// plainly. Both surfaces owe the steward that sentence — a steward revoking
// a photographed sheet from their kitchen has no other way to learn it.
func TestRevokingTheActiveCardSaysTheBoxGoesDark(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)
	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/force-activate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup force-activate: status = %d", rec.Code)
	}

	// A pending card's confirmation is about the card only.
	pending := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens/3/revoke/confirm", c)
	if pending.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", pending.Code)
	}
	if strings.Contains(pending.Body.String(), "stops anyone leaving or taking") {
		t.Error("a pending card's confirmation claims leaving or taking stops")
	}

	// The active card's is about the shelf.
	active := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens/1/revoke/confirm", c)
	if !strings.Contains(active.Body.String(), "stops anyone leaving or taking") {
		t.Errorf("the active card's confirmation does not say leaving or taking stops:\n%s", active.Body.String())
	}

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/revoke", nil, c)
	if got := rec.Header().Get("Location"); !strings.HasSuffix(got, "?ok=revoked-active") {
		t.Fatalf("Location = %q, want ok=revoked-active", got)
	}
	follow := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens?ok=revoked-active", c)
	if !strings.Contains(follow.Body.String(), okMessages["revoked-active"]) {
		t.Errorf("the tokens page does not carry the revoked-active message:\n%s", follow.Body.String())
	}
}

// A revoke that is not of the active card keeps the plainer confirmation:
// that card stopped working, nothing else did.
func TestRevokingAPendingCardKeepsThePlainConfirmation(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/4/revoke", nil, c)
	if got := rec.Header().Get("Location"); !strings.HasSuffix(got, "?ok=revoked") {
		t.Errorf("Location = %q, want ok=revoked", got)
	}
}

// ExtendToken only ever succeeds on the active card; every pending card in
// a fresh booklet is a card that has never been put in the door.
func TestExtendRefusalNamesTheState(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/3/extend", nil, c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "card in the door") {
		t.Errorf("the refusal does not name why the card can't be extended:\n%s", rec.Body.String())
	}
}

// A second revoke of the same card must refuse rather than silently
// succeed, and must say which state it found.
func TestRevokeTwiceIsRefused(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/revoke", nil, c)
	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/revoke", nil, c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already revoked") {
		t.Errorf("the refusal does not name the state it found:\n%s", rec.Body.String())
	}
}

// A non-numeric period is attacker-controllable path input on an
// authenticated admin surface, and must render the shared invalid-period
// refusal rather than panic or 500.
func TestNonNumericPeriodIsRefusedNotPanicked(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/September/revoke", nil, c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// html/template escapes the apostrophe in invalidPeriodMsg (' -> &#39;),
	// so this checks for the message's substance rather than its exact
	// bytes — the point is that a junk path segment produces this rendered
	// refusal, not a panic or a 500.
	if !strings.Contains(rec.Body.String(), "That card number") {
		t.Errorf("body does not carry the invalid-period message:\n%s", rec.Body.String())
	}
}

// A GET at a POST-only route is a byte-identical 404, never a 405.
func TestTokenMutationsAre404ToAGET(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()
	for _, p := range []string{
		"/b/" + lib.Slug + "/steward/tokens/3/force-activate",
		"/b/" + lib.Slug + "/steward/tokens/3/extend",
		"/b/" + lib.Slug + "/steward/tokens/3/revoke",
		"/b/" + lib.Slug + "/steward/tokens/rotate",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "" {
			t.Errorf("GET %s set Allow: %q", p, allow)
		}
	}
}

// The destructive control comes last and is not a mis-tap from a benign one.
//
// "Start a new booklet…" used to sit twelve pixels under the full-width
// "Download the booklet" button, with "Back to the hub" after it — the two
// most different actions on the page adjacent, on a surface specified for
// one-handed outdoor use.
func TestRotateControlComesLastAndApart(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	body := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens",
		loginAsSteward(t, h, lib, key)).Body.String()

	download := strings.Index(body, "Download the booklet")
	hub := strings.Index(body, "Back to the hub")
	rotate := strings.Index(body, "Start a new booklet")
	for name, i := range map[string]int{"download": download, "hub": hub, "rotate": rotate} {
		if i < 0 {
			t.Fatalf("%s control missing from the page", name)
		}
	}
	if rotate < hub {
		t.Error("the destructive control is above 'Back to the hub'; it must come last")
	}
	if rotate < download {
		t.Error("the destructive control is above 'Download the booklet'; it must come last of all three")
	}

	layout, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if !strings.Contains(string(layout), "a.destructive") {
		t.Error("layout.html defines no a.destructive class")
	}
}

// seedWebTokens inserts a full booklet directly through the store, the same
// fixture style stewardServerWithItems uses for items — these tests have no
// reason to drive the booklet command over HTTP.
func seedWebTokens(t *testing.T, st *store.Store, lib boulevard.Library) []boulevard.Token {
	t.Helper()
	out := make([]boulevard.Token, 0, tokens.PeriodCount)
	for _, p := range tokens.Periods(boulevard.NewDate(2026, time.August, 14), tokens.PeriodCount) {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, boulevard.Token{
			ID: id, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	if err := st.InsertTokens(context.Background(), lib.ID, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The desk must go on calling a card by the month printed on it after
// force-activate has moved its dates.
//
// Found by the Milestone 4b acceptance run: an October card put in the door
// early showed as "August 2026" on the tokens page, so the row and the card
// in the steward's hand could not be matched. Worse, the rotate confirmation
// named that wrong month while asking whether to discard secrets — a page
// whose only job is to say which card survives.
func TestTokensPageKeepsThePrintedMonthAfterForceActivate(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	// Period 3 is October 2026 in seedWebTokens' fixture.
	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/3/force-activate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("force-activate: status = %d, want 303", rec.Code)
	}

	body := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens", c).Body.String()
	if !strings.Contains(body, "October 2026") {
		t.Errorf("the tokens page no longer calls the card October 2026:\n%s", body)
	}

	// And the confirmation that asks about discarding secrets must name the
	// same card the steward is holding.
	confirm := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/tokens/rotate/confirm", c).Body.String()
	if !strings.Contains(confirm, "October 2026") {
		t.Errorf("the rotate confirmation misnames the card in the door:\n%s", confirm)
	}
}
