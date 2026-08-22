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
