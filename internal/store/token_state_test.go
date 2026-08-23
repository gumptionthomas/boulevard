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

// A pending card can have granted a session, and rotation has to survive it.
//
// RecordScan marks a card active only when its index exceeds the current
// active one, so a lower card scanned inside its own window grants a session
// and stays pending — the stray-card-found-in-a-drawer case DESIGN.md §4
// describes, reached the moment a steward force-activates a later card.
// sessions.token_id is NOT NULL REFERENCES tokens(id) and foreign keys are
// on, so deleting that row outright fails the constraint, and rotation — the
// operation a steward reaches for precisely when the sheet has been
// photographed — is the one that refuses.
func TestRotateSurvivesAPendingCardThatGrantedASession(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)

	// The steward swaps November's card in early, so period 4 is the one in
	// the door and periods 1-3 stay pending with their windows still open.
	if err := s.ForceActivateToken(ctx, lib.ID, 4, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatalf("ForceActivateToken: %v", err)
	}

	// Someone scans September's card inside its own window. It grants a
	// session but must not rewind the active card, so it stays pending.
	scan := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	if err := s.RecordScan(ctx, lib.ID, toks[1], scan); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	if got := stateOf(t, s, lib, 2); got.State != boulevard.TokenPending {
		t.Fatalf("setup: period 2 state = %q, want pending", got.State)
	}
	sessID, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, boulevard.Session{
		ID: sessID, LibraryID: lib.ID, TokenID: toks[1].ID,
		CreatedAt: scan, ExpiresAt: scan.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	existing, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary: %v", err)
	}
	rot := tokens.PlanRotation(existing, boulevard.NewDate(2026, time.September, 3))
	if err := s.RotatePendingTokens(ctx, lib.ID, buildRotation(t, lib.ID, rot)); err != nil {
		t.Fatalf("RotatePendingTokens: %v", err)
	}

	// The seen card keeps its row, so the session's foreign key still
	// resolves, but its secret no longer opens anything.
	if seen := stateOf(t, s, lib, 2); seen.State != boulevard.TokenRevoked {
		t.Errorf("period 2 state = %q, want revoked", seen.State)
	}
	if _, err := s.SessionByID(ctx, sessID, scan.Add(time.Hour)); err != nil {
		t.Errorf("SessionByID: %v — the live session must outlive the rotation", err)
	}
	if got := stateOf(t, s, lib, 4); got.State != boulevard.TokenActive {
		t.Errorf("period 4 state = %q, want active", got.State)
	}
	// Every never-scanned pending card is gone, indices and all.
	after, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary: %v", err)
	}
	for _, tok := range after {
		if tok.PeriodIndex < tokens.PeriodCount+1 && tok.PeriodIndex != 2 && tok.PeriodIndex != 4 {
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

// The month a card is identified by must survive every operation that
// rewrites its dates.
//
// This is the defect the Milestone 4b acceptance run found: force-activate
// rewrites valid_from, the month label was derived from valid_from, and so
// putting an October card in the door early made the desk call it August —
// including on the confirmation that asks whether to discard secrets while
// naming the card it will keep. DESIGN.md §4 accepts the date divergence
// precisely because the month name keeps the card in the steward's hand
// identifiable, so the label has to come from somewhere neither
// force-activate nor extend touches.
func TestPrintedLabelSurvivesForceActivateAndExtend(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)

	// Period 3 is October 2026 in seededLibrary's fixture.
	printed := stateOf(t, s, lib, 3).PrintedFrom
	if printed.Month != time.October {
		t.Fatalf("fixture: period 3 prints as %s, want October", printed.Month)
	}

	if err := s.ForceActivateToken(ctx, lib.ID, 3, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatalf("ForceActivateToken: %v", err)
	}
	after := stateOf(t, s, lib, 3)
	if !after.ValidFrom.Equal(boulevard.NewDate(2026, time.August, 20)) {
		t.Fatalf("force-activate did not move valid_from: %s", after.ValidFrom)
	}
	if !after.PrintedFrom.Equal(printed) {
		t.Errorf("printed label moved to %s; the card in the steward's hand still says %s",
			after.PrintedFrom, printed)
	}

	if _, err := s.ExtendToken(ctx, lib.ID, 3); err != nil {
		t.Fatalf("ExtendToken: %v", err)
	}
	if got := stateOf(t, s, lib, 3).PrintedFrom; !got.Equal(printed) {
		t.Errorf("extend moved the printed label to %s, want %s", got, printed)
	}
}

// Rotation mints cards that have never been printed, so their label is
// simply their own period — but it must be recorded, not left blank, or the
// first force-activate on the new booklet reintroduces the defect.
func TestRotatedCardsRecordTheirPrintedLabel(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, _ := seededLibrary(t, s)

	existing, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	rot := tokens.PlanRotation(existing, boulevard.NewDate(2026, time.August, 20))
	if err := s.RotatePendingTokens(ctx, lib.ID, buildRotation(t, lib.ID, rot)); err != nil {
		t.Fatalf("RotatePendingTokens: %v", err)
	}

	for i := rot.StartIndex; i < rot.StartIndex+tokens.PeriodCount; i++ {
		tok := stateOf(t, s, lib, i)
		if tok.PrintedFrom.Year == 0 {
			t.Fatalf("period %d has no printed label", i)
		}
		if !tok.PrintedFrom.Equal(tok.ValidFrom) {
			t.Errorf("period %d prints as %s but starts %s; a freshly minted card should match",
				i, tok.PrintedFrom, tok.ValidFrom)
		}
	}
}
