package main

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// seedTokens inserts a full booklet, mirroring what `boulevard booklet`
// writes at install. Returned so a test can assert on the secrets that must
// never be printed.
func seedTokens(t *testing.T, s *store.Store, lib boulevard.Library) []boulevard.Token {
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
	if err := s.InsertTokens(context.Background(), lib.ID, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A card is addressed by its period number, because that is what the
// steward is holding: the card says "September" and the booklet numbers it
// CARD 9 OF 12.
func TestTokensListsTheBooklet(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	toks := seedTokens(t, s, libs[0])

	out := captureStdout(t, func() {
		if code := runTokens([]string{"--db", path}); code != exitOK {
			t.Errorf("tokens exit = %d, want %d", code, exitOK)
		}
	})

	for _, want := range []string{"CARD 1", "CARD 12", "pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// A secret is the shelf's write credential, and this output is what a
	// steward pastes into a support thread.
	for _, tok := range toks {
		if strings.Contains(out, tok.Secret) {
			t.Fatalf("a token secret reached stdout:\n%s", out)
		}
	}
}

func TestTokensMarksTheCardInTheDoor(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])
	if err := s.ForceActivateToken(context.Background(), libs[0].ID, 3,
		boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if code := runTokens([]string{"--db", path}); code != exitOK {
			t.Errorf("tokens exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "active") {
		t.Errorf("no active card marked:\n%s", out)
	}
}

func TestForceActivateRefusesACardThatIsNotPending(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])

	if code := runForceActivate([]string{"--db", path, "3"}); code != exitOK {
		t.Fatalf("first force-activate exit = %d, want %d", code, exitOK)
	}
	if code := runForceActivate([]string{"--db", path, "3"}); code == exitOK {
		t.Error("force-activating an already-active card succeeded")
	}
}

func TestExtendRefusesACardThatIsNotInTheDoor(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])
	if code := runExtend([]string{"--db", path, "2"}); code == exitOK {
		t.Error("extended a card that is not active")
	}
}

func TestRevokeRefusesTwice(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])

	if code := runRevoke([]string{"--db", path, "4"}); code != exitOK {
		t.Fatalf("first revoke exit = %d, want %d", code, exitOK)
	}
	if code := runRevoke([]string{"--db", path, "4"}); code == exitOK {
		t.Error("revoking twice succeeded")
	}
}

func TestCardNumberMustBeANumber(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])
	if code := runRevoke([]string{"--db", path, "September"}); code != exitUsage {
		t.Errorf("exit = %d, want %d for a non-numeric card", code, exitUsage)
	}
}
