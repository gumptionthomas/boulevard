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
