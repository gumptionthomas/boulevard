package store

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// Force-activate is asserted through Validate, not through the row.
//
// tokens.Validate reads state only for "revoked" — everything else is the
// date window. An implementation that set state = active and stopped would
// leave the card answering NotYet, and a test asserting state == active
// would pass while the feature did nothing at all.
func TestForceActivateMakesACardScannableBeyondTheGrace(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)

	// Period 3 is October 2026; 20 August is far outside its window plus
	// the 7-day grace, so nothing but a date change can make it scan.
	today := boulevard.NewDate(2026, time.August, 20)

	before := stateOf(t, s, lib, 3)
	if got := tokens.Validate(before, today, tokens.GraceDays); got != tokens.NotYet {
		t.Fatalf("before: Validate = %v, want NotYet", got)
	}

	if err := s.ForceActivateToken(ctx, lib.ID, 3, today); err != nil {
		t.Fatalf("ForceActivateToken: %v", err)
	}

	after := stateOf(t, s, lib, 3)
	if got := tokens.Validate(after, today, tokens.GraceDays); got != tokens.Granted {
		t.Errorf("after: Validate = %v, want Granted", got)
	}
	if after.State != boulevard.TokenActive {
		t.Errorf("state = %q, want active", after.State)
	}
	if !after.ValidFrom.Equal(today) {
		t.Errorf("valid_from = %s, want %s", after.ValidFrom, today)
	}
	if !after.ValidUntil.Equal(before.ValidUntil) {
		t.Errorf("valid_until = %s, want it unchanged at %s", after.ValidUntil, before.ValidUntil)
	}
}

func TestForceActivateExpiresThePreviousActiveCard(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)

	// Scan period 1 so something is active.
	if err := s.RecordScan(ctx, lib.ID, toks[0], time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	if got := stateOf(t, s, lib, 1); got.State != boulevard.TokenActive {
		t.Fatalf("setup: period 1 state = %q, want active", got.State)
	}

	if err := s.ForceActivateToken(ctx, lib.ID, 3, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatalf("ForceActivateToken: %v", err)
	}

	if got := stateOf(t, s, lib, 1); got.State != boulevard.TokenExpired {
		t.Errorf("period 1 state = %q, want expired", got.State)
	}
	if got := stateOf(t, s, lib, 3); got.State != boulevard.TokenActive {
		t.Errorf("period 3 state = %q, want active", got.State)
	}
}

func TestForceActivateRefusesANonPendingCard(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)
	today := boulevard.NewDate(2026, time.August, 20)

	if err := s.ForceActivateToken(ctx, lib.ID, 3, today); err != nil {
		t.Fatalf("first ForceActivateToken: %v", err)
	}
	err := s.ForceActivateToken(ctx, lib.ID, 3, today)
	if !errors.Is(err, ErrNotPending) {
		t.Errorf("err = %v, want ErrNotPending", err)
	}
}

func TestForceActivateUnknownPeriodIsNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)
	err := s.ForceActivateToken(ctx, lib.ID, 99, boulevard.NewDate(2026, time.August, 20))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestExtendMovesOnlyTheActiveCardsEndDate(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	if err := s.RecordScan(ctx, lib.ID, toks[0], time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}

	before2 := stateOf(t, s, lib, 2)

	// Period 1 is 14-31 August 2026; extending lands on 30 September.
	got, err := s.ExtendToken(ctx, lib.ID, 1)
	if err != nil {
		t.Fatalf("ExtendToken: %v", err)
	}
	want := boulevard.NewDate(2026, time.September, 30)
	if !got.Equal(want) {
		t.Errorf("returned %s, want %s", got, want)
	}
	if stored := stateOf(t, s, lib, 1); !stored.ValidUntil.Equal(want) {
		t.Errorf("stored valid_until = %s, want %s", stored.ValidUntil, want)
	}

	// A second call is relative to the value it finds.
	if got, err = s.ExtendToken(ctx, lib.ID, 1); err != nil {
		t.Fatalf("second ExtendToken: %v", err)
	}
	if want = boulevard.NewDate(2026, time.October, 31); !got.Equal(want) {
		t.Errorf("second extend returned %s, want %s", got, want)
	}

	// Later periods keep the dates already printed on their cards.
	after2 := stateOf(t, s, lib, 2)
	if !after2.ValidFrom.Equal(before2.ValidFrom) || !after2.ValidUntil.Equal(before2.ValidUntil) {
		t.Errorf("period 2 moved: %s..%s, want %s..%s",
			after2.ValidFrom, after2.ValidUntil, before2.ValidFrom, before2.ValidUntil)
	}
}

func TestExtendRefusesACardThatIsNotActive(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)
	_, err := s.ExtendToken(ctx, lib.ID, 2)
	if !errors.Is(err, ErrNotActive) {
		t.Errorf("err = %v, want ErrNotActive", err)
	}
}

func TestRevokeStopsTheCardScanning(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	if err := s.RecordScan(ctx, lib.ID, toks[0], time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	today := boulevard.NewDate(2026, time.August, 20)

	if got := tokens.Validate(stateOf(t, s, lib, 1), today, tokens.GraceDays); got != tokens.Granted {
		t.Fatalf("before: Validate = %v, want Granted", got)
	}
	if err := s.RevokeToken(ctx, lib.ID, 1); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if got := tokens.Validate(stateOf(t, s, lib, 1), today, tokens.GraceDays); got != tokens.Invalid {
		t.Errorf("after: Validate = %v, want Invalid", got)
	}
}

func TestRevokeTwiceIsRefused(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)
	if err := s.RevokeToken(ctx, lib.ID, 4); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	err := s.RevokeToken(ctx, lib.ID, 4)
	if !errors.Is(err, ErrAlreadyRevoked) {
		t.Errorf("err = %v, want ErrAlreadyRevoked", err)
	}
}

// Rotation replaces the unprinted cards and leaves the one in the door
// alone. The discarded pending cards' indices are not reused.
func TestRotateReplacesPendingAndKeepsTheActiveCard(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	if err := s.RecordScan(ctx, lib.ID, toks[0], time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	active := stateOf(t, s, lib, 1)

	existing, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary: %v", err)
	}
	rot := tokens.PlanRotation(existing, boulevard.NewDate(2026, time.August, 20))
	fresh := buildRotation(t, lib.ID, rot)

	if err := s.RotatePendingTokens(ctx, lib.ID, fresh); err != nil {
		t.Fatalf("RotatePendingTokens: %v", err)
	}

	after, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary: %v", err)
	}
	if len(after) != tokens.PeriodCount+1 {
		t.Fatalf("%d tokens after rotate, want %d (twelve new plus the active card)",
			len(after), tokens.PeriodCount+1)
	}

	stillActive := stateOf(t, s, lib, 1)
	if stillActive.Secret != active.Secret {
		t.Error("the active card's secret changed; the card in the door must keep working")
	}
	if stillActive.State != boulevard.TokenActive {
		t.Errorf("active card state = %q, want active", stillActive.State)
	}

	for i := 13; i <= 24; i++ {
		tok := stateOf(t, s, lib, i)
		if tok.State != boulevard.TokenPending {
			t.Errorf("period %d state = %q, want pending", i, tok.State)
		}
	}
	// The old pending cards are gone, indices and all.
	for _, tok := range after {
		if tok.PeriodIndex >= 2 && tok.PeriodIndex <= 12 {
			t.Errorf("period %d survived the rotation", tok.PeriodIndex)
		}
	}
}

// buildRotation turns a Rotation into twelve insertable tokens, the way
// every caller of RotatePendingTokens must: the store persists tokens, it
// does not mint secrets (the same split InsertTokens already uses).
func buildRotation(t *testing.T, libID boulevard.LibraryID, rot tokens.Rotation) []boulevard.Token {
	t.Helper()
	out := make([]boulevard.Token, 0, tokens.PeriodCount)
	for _, p := range tokens.Periods(rot.Start, tokens.PeriodCount) {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, boulevard.Token{
			ID: id, LibraryID: libID, Secret: secret,
			PeriodIndex: rot.StartIndex + p.Index - 1,
			ValidFrom:   p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	return out
}
