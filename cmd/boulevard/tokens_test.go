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

// A card is addressed by the bracketed handle `boulevard tokens` prints
// beside it, not by the friendly "Card N" label next to that handle — the
// two are the same number only until a library rotates. See cardLabel's
// comment in tokens.go and TestTokensHandleSurvivesARotation below.
func TestTokensListsTheBooklet(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	toks := seedTokens(t, s, libs[0])

	out := captureStdout(t, func() {
		if code := runTokens([]string{"--db", path}); code != exitOK {
			t.Errorf("tokens exit = %d, want %d", code, exitOK)
		}
	})

	for _, want := range []string{"[1]", "[12]", "Card 1", "Card 12", "pending"} {
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

// The handle it prints must be the handle it accepts — the same rule
// `boulevard queue` follows for item ids (queue_cli_test.go). tokens.go's
// listing shows two numbers per card: the bracketed handle (period_index,
// monotonic across booklets) and a friendlier "Booklet N · Card M" label
// (the card's on-paper position, which restarts at 1 every booklet). Only
// the bracketed handle may be typed at force-activate, extend or revoke;
// this test rotates a library so the two numbers diverge and checks both
// directions of that rule.
func TestTokensHandleSurvivesARotation(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	seedTokens(t, s, lib)

	if code := runForceActivate([]string{"--db", path, "1"}); code != exitOK {
		t.Fatalf("force-activate exit = %d, want %d", code, exitOK)
	}

	existing, err := s.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	rot := tokens.PlanRotation(existing, boulevard.NewDate(2026, time.August, 20))
	fresh := make([]boulevard.Token, 0, tokens.PeriodCount)
	for _, p := range tokens.Periods(rot.Start, tokens.PeriodCount) {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		fresh = append(fresh, boulevard.Token{
			ID: id, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: rot.StartIndex + p.Index - 1,
			ValidFrom:   p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	if err := s.RotatePendingTokens(context.Background(), lib.ID, fresh); err != nil {
		t.Fatal(err)
	}
	if rot.StartIndex != 13 {
		t.Fatalf("test setup: rotation started at %d, want 13", rot.StartIndex)
	}

	out := captureStdout(t, func() {
		if code := runTokens([]string{"--db", path}); code != exitOK {
			t.Errorf("tokens exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "[13]") || !strings.Contains(out, "Booklet 2 · Card 1") {
		t.Fatalf("listing did not show booklet 2 card 1's handle and label:\n%s", out)
	}

	// The handle the listing printed for booklet 2's first card is 13, and
	// that is what force-activate must accept.
	if code := runForceActivate([]string{"--db", path, "13"}); code != exitOK {
		t.Errorf("the handle the listing printed did not resolve: exit %d, want %d", code, exitOK)
	}
	// The number printed ON the card ("Card 1") must NOT also work as an
	// argument: typing "1" addresses booklet 1's card 1, not booklet 2's,
	// and by this point in the test it is no longer pending (force-activate
	// above expired it), so it correctly refuses either way.
	if code := runForceActivate([]string{"--db", path, "1"}); code == exitOK {
		t.Error("the on-paper card number resolved a different card than its handle — it must not")
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

// Spec §3: revoking the active card stops the box working until someone
// walks to it with a different card, and the confirmation says so plainly
// rather than softening it. A steward revoking a photographed sheet from a
// terminal has no other way to learn that the box has gone dark.
func TestRevokingTheActiveCardSaysTheBoxGoesDark(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])
	if code := runForceActivate([]string{"--db", path, "2"}); code != exitOK {
		t.Fatalf("setup force-activate exit = %d, want %d", code, exitOK)
	}

	out := captureStdout(t, func() {
		if code := runRevoke([]string{"--db", path, "2"}); code != exitOK {
			t.Fatalf("revoke exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "card in the door") || !strings.Contains(out, "left or taken") {
		t.Errorf("revoking the active card does not say the box goes dark:\n%s", out)
	}

	// A card that was not in the door costs nothing beyond itself, and must
	// not claim otherwise.
	plain := captureStdout(t, func() {
		if code := runRevoke([]string{"--db", path, "5"}); code != exitOK {
			t.Fatalf("revoke exit = %d, want %d", code, exitOK)
		}
	})
	if strings.Contains(plain, "left or taken") {
		t.Errorf("revoking a pending card claims the box went dark:\n%s", plain)
	}
}

func TestCardNumberMustBeANumber(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])
	if code := runRevoke([]string{"--db", path, "September"}); code != exitUsage {
		t.Errorf("exit = %d, want %d for a non-numeric card", code, exitUsage)
	}
}
