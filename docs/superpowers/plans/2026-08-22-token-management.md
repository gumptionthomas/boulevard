# Milestone 4b — Token Management and Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the steward the six token operations (see, download, force-activate, extend, revoke, rotate) and an export that is a runnable database file rather than a format.

**Architecture:** Pure period arithmetic lands in `internal/tokens`, persistence in `internal/store`, and both surfaces (web desk and CLI) sit on top — the same layering Milestones 3 and 4a used. Export opens a second `store.Store` at the target path, which gives it the migrations and the `0600` mode for free, then copies three tables and deliberately skips three.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (pure Go), stdlib `net/http` pattern routing, `html/template` behind `go:embed`, `github.com/skip2/go-qrcode` and `github.com/go-pdf/fpdf` (both already dependencies).

**Spec:** `docs/superpowers/specs/2026-08-22-token-management-design.md` — read it before Task 1. The plan argues from it and does not repeat its reasoning.

## Global Constraints

- **`tokens.Validate` reads `state` only for `revoked`.** Everything else is the date window. An operation meant to change whether a card scans must move a date or set `revoked`; writing `state` alone is a no-op.
- **Token secrets are plaintext and are the shelf's write credential.** Never hash them, never log them, never put one in an error message. Files holding them are `0600`.
- **Clocks are injected.** `web.New` takes `now func() time.Time`; `tokens.Validate` takes `today`. Never call `time.Now()` inside a handler or a pure function. Tests that format a time use a non-UTC location (`chicago(t)` in `internal/store`, an injected clock in `internal/web`), because the store round-trips timestamps through RFC3339 in UTC.
- **`SetMaxOpenConns(1)`.** An unclosed `*sql.Rows` held open across another query on the same `*sql.DB` is a **hang**, not an error. Read rows fully into a slice before issuing the next query.
- **No store method infers a current library.** Every method here takes an explicit `boulevard.LibraryID`.
- **Steward mutations are POST-only**, and a GET at one returns a byte-identical 404 via `hideUnmatchedMethods`. Do not add GET forms.
- **No free-text message parameters in redirects.** Confirmations use the closed `okMessages` map keyed by an enumerated `?ok=` code.
- **Every steward route 404s until a steward key exists.** Use `requireSteward`, which enforces it.
- **`go vet ./...` and `gofmt -l .` must both be clean before every commit.**
- **Run the whole suite** (`go test ./...`); it is fast.

## File Structure

| File | Responsibility |
|---|---|
| `internal/booklet/plan.go` | *modified* — a card's printed number is its position in the booklet |
| `internal/tokens/rotate.go` | *new* — pure: where the next booklet starts |
| `internal/store/token.go` | *modified* — four state transitions |
| `internal/store/store.go` | *modified* — two new error sentinels |
| `internal/store/export.go` | *new* — `CopyLibraryTo` |
| `internal/web/steward_tokens.go` | *new* — tokens page, five POSTs, the PDF download |
| `internal/web/steward_export.go` | *new* — export page and download |
| `internal/web/templates/steward-tokens.html` | *new* |
| `internal/web/templates/steward-export.html` | *new* |
| `cmd/boulevard/tokens.go` | *new* — `tokens`, `force-activate`, `extend`, `revoke` |
| `cmd/boulevard/export.go` | *new* — `export` |
| `cmd/boulevard/booklet.go` | *modified* — `--rotate` |

---

### Task 1: A card's printed number is its position, not its period index

**Files:**
- Modify: `internal/booklet/plan.go:88-105`
- Test: `internal/booklet/plan_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `placeCard(tok boulevard.Token, r Rect, baseURL string, number, of int) PlacedCard`. Task 3 makes rotation mint period indices above 12; without this, cards print "CARD 13 OF 12".

- [ ] **Step 1: Write the failing test**

Add to `internal/booklet/plan_test.go`:

```go
// A rotated booklet carries period indices above 12 (Task 3 mints 13-24).
// The number printed on a card is its position in the booklet being laid
// out, so a second booklet still reads CARD 1 OF 12 through CARD 12 OF 12.
func TestCardNumbersComeFromPositionNotPeriodIndex(t *testing.T) {
	lib := boulevard.Library{Slug: "fairview", Name: "Fairview", BaseURL: "https://example.org"}
	periods := tokens.Periods(boulevard.NewDate(2027, time.September, 1), tokens.PeriodCount)

	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		toks = append(toks, boulevard.Token{
			Secret:      "SECRET",
			PeriodIndex: p.Index + 12, // 13..24, as after one rotation
			ValidFrom:   p.From,
			ValidUntil:  p.Until,
		})
	}

	plan, err := BuildPlan(Input{Library: lib, Tokens: toks})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	var got []int
	for _, sheet := range plan.Sheets {
		for _, c := range sheet.Cards {
			got = append(got, c.Index)
			if c.Of != tokens.PeriodCount {
				t.Errorf("Of = %d, want %d", c.Of, tokens.PeriodCount)
			}
		}
	}
	if len(got) != tokens.PeriodCount {
		t.Fatalf("laid out %d cards, want %d", len(got), tokens.PeriodCount)
	}
	for i, n := range got {
		if n != i+1 {
			t.Errorf("card %d printed number %d, want %d", i, n, i+1)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test -run TestCardNumbersComeFromPositionNotPeriodIndex ./internal/booklet`
Expected: FAIL — `card 0 printed number 13, want 1`.

- [ ] **Step 3: Make the number a parameter**

In `internal/booklet/plan.go`, replace the `placeCard` function and both of its call sites:

```go
// placeCard positions one token's card. "number" is the card's position in
// the booklet being laid out and "of" is that booklet's size — neither is
// read from the token, so a second booklet (whose period indices continue
// at 13) still prints CARD 1 OF 12 rather than CARD 13 OF 12.
func placeCard(tok boulevard.Token, r Rect, baseURL string, number, of int) PlacedCard {
	return PlacedCard{
		Rect:    r,
		Month:   tok.ValidFrom.MonthName(),
		Year:    tok.ValidFrom.Year,
		From:    tok.ValidFrom,
		Until:   tok.ValidUntil,
		Index:   number,
		Of:      of,
		Payload: baseURL + "/s/" + tok.Secret,
	}
}
```

Sheet 1's loop becomes:

```go
	for i := 0; i < CardsPerSheet; i++ {
		sheet1.Cards = append(sheet1.Cards, placeCard(toks[i], CardRect(i), in.Library.BaseURL, i+1, len(toks)))
	}
```

Sheet 2's loop becomes:

```go
	for i := CardsPerSheet; i < len(toks); i++ {
		slot := CardsPerSheet - Cols + (i - CardsPerSheet) // final row of the grid
		sheet2.Cards = append(sheet2.Cards, placeCard(toks[i], CardRect(slot), in.Library.BaseURL, i+1, len(toks)))
	}
```

`toks` is already sorted by `PeriodIndex` immediately above, so `i+1` is the position.

- [ ] **Step 4: Run the package's whole suite**

Run: `go test ./internal/booklet`
Expected: PASS, **including the golden-PDF test**. For an unrotated booklet `i+1 == PeriodIndex`, so the rendered bytes are unchanged. If the golden test fails, the change is wrong — do not re-record the fixture.

- [ ] **Step 5: Commit**

```bash
git add internal/booklet/plan.go internal/booklet/plan_test.go
git commit -m "fix: a card's printed number is its position in the booklet"
```

---

### Task 2: Force-activate, extend and revoke in the store

**Files:**
- Modify: `internal/store/store.go` (two sentinels)
- Modify: `internal/store/token.go` (three methods)
- Test: `internal/store/token_test.go`

**Interfaces:**
- Consumes: `openTemp(t)`, `seededLibrary(t, s) (boulevard.Library, []boulevard.Token)`, `stateOf(t, s, lib, periodIndex) boulevard.Token` — all existing helpers in `internal/store/store_test.go`. `seededLibrary` inserts twelve `pending` tokens whose periods start 14 August 2026.
- Produces:
  - `func (s *Store) ForceActivateToken(ctx context.Context, id boulevard.LibraryID, periodIndex int, today boulevard.Date) error`
  - `func (s *Store) ExtendToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) (boulevard.Date, error)` — returns the new `valid_until`
  - `func (s *Store) RevokeToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) error`
  - `store.ErrNotActive`, `store.ErrAlreadyRevoked`
  - Reuses the existing `store.ErrNotFound` and `store.ErrNotPending`.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/token_state_test.go`:

```go
package store

import (
	"context"
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
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/store`
Expected: FAIL to compile — `s.ForceActivateToken undefined`.

- [ ] **Step 3: Add the two sentinels**

In `internal/store/store.go`, beside the existing sentinels:

```go
// ErrNotActive is returned when an operation that only makes sense on the
// card currently in the door is aimed at a different one.
var ErrNotActive = errors.New("token is not the active card")

// ErrAlreadyRevoked distinguishes "nothing to do" from a refusal a steward
// should act on. Revoking is irreversible, so saying so is kinder than
// silently succeeding twice.
var ErrAlreadyRevoked = errors.New("token is already revoked")
```

- [ ] **Step 4: Implement the three methods**

Append to `internal/store/token.go`:

```go
// ForceActivateToken promotes a pending card, for the steward who swapped
// the card early (DESIGN.md §4).
//
// It moves valid_from to today, and that is the whole point rather than a
// side effect. tokens.Validate reads state only for "revoked"; everything
// else is the date window. Setting state = active on a card whose period
// has not started leaves it answering NotYet — the command would report
// success and change nothing a scanner can see. Inside the 7-day grace the
// command is redundant anyway, because the card already scans, so the only
// case that reaches here is one where a date has to move.
//
// valid_until is deliberately not moved: the card ends when it was always
// going to end. The printed card will now disagree with the database, which
// is accepted — the steward has physically put that card in the door, and
// the month name is how they identify it.
func (s *Store) ForceActivateToken(ctx context.Context, id boulevard.LibraryID, periodIndex int, today boulevard.Date) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin force-activate: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) != boulevard.TokenPending {
		return fmt.Errorf("period %d is %s: %w", periodIndex, state, ErrNotPending)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ? WHERE library_id = ? AND state = ?`,
		string(boulevard.TokenExpired), string(id), string(boulevard.TokenActive)); err != nil {
		return fmt.Errorf("expire the previous active token: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ?, valid_from = ? WHERE library_id = ? AND period_index = ?`,
		string(boulevard.TokenActive), today.String(), string(id), periodIndex); err != nil {
		return fmt.Errorf("activate period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit force-activate: %w", err)
	}
	return nil
}

// ExtendToken pushes the active card's end date to the last day of the
// month after the one it currently ends in, and returns that date so the
// caller can name it. For the steward whose booklet is lost and whose
// replacement is not printed yet (DESIGN.md §4).
//
// Later periods are deliberately untouched. Cascading the shift would keep
// exactly one card valid at a time but would make every unswapped printed
// card disagree with the database — the card reading "September" would
// carry October's period. Two cards valid at once is the smaller problem,
// and one this design already accepts: the 7-day grace on both ends means
// adjacent cards overlap by fourteen days regardless.
func (s *Store) ExtendToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) (boulevard.Date, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("begin extend: %w", err)
	}
	defer tx.Rollback()

	var state, until string
	err = tx.QueryRowContext(ctx,
		`SELECT state, valid_until FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Date{}, ErrNotFound
	}
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) != boulevard.TokenActive {
		return boulevard.Date{}, fmt.Errorf("period %d is %s: %w", periodIndex, state, ErrNotActive)
	}

	cur, err := boulevard.ParseDate(until)
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("parse valid_until %q: %w", until, err)
	}
	next := cur.NextMonth().LastOfMonth()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET valid_until = ? WHERE library_id = ? AND period_index = ?`,
		next.String(), string(id), periodIndex); err != nil {
		return boulevard.Date{}, fmt.Errorf("extend period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return boulevard.Date{}, fmt.Errorf("commit extend: %w", err)
	}
	return next, nil
}

// RevokeToken burns one card's secret, for a sheet that was stolen or
// photographed (DESIGN.md §4). "revoked" is the one state tokens.Validate
// reads, and §4 requires a revoked secret to produce a response
// byte-identical to an unknown one — already true, and pinned by
// TestScanRevokedIsIndistinguishableFromUnknown.
//
// There is no un-revoke. The way forward is force-activating the next card
// or rotating.
func (s *Store) RevokeToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) == boulevard.TokenRevoked {
		return ErrAlreadyRevoked
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ? WHERE library_id = ? AND period_index = ?`,
		string(boulevard.TokenRevoked), string(id), periodIndex); err != nil {
		return fmt.Errorf("revoke period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke: %w", err)
	}
	return nil
}
```

Add `"database/sql"` and `"errors"` to `internal/store/token.go`'s imports if they are not already there.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/store`
Expected: PASS.

- [ ] **Step 6: Run everything, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/store/store.go internal/store/token.go internal/store/token_state_test.go
git commit -m "feat: force-activate, extend and revoke a card"
```

---

### Task 3: Rotation — twelve fresh future cards

**Files:**
- Create: `internal/tokens/rotate.go`
- Create: `internal/tokens/rotate_test.go`
- Modify: `internal/store/token.go` (one method)
- Test: `internal/store/token_state_test.go`

**Interfaces:**
- Consumes: `tokens.Periods(install boulevard.Date, n int) []Period` where `Period{Index int; From, Until boulevard.Date}` and `Index` starts at 1; `tokens.PeriodCount = 12`.
- Produces:
  - `func tokens.PlanRotation(existing []boulevard.Token, today boulevard.Date) Rotation` with `type Rotation struct { StartIndex int; Start boulevard.Date }`
  - `func (s *Store) RotatePendingTokens(ctx context.Context, id boulevard.LibraryID, toks []boulevard.Token) error`

- [ ] **Step 1: Write the failing test for the pure planner**

Create `internal/tokens/rotate_test.go`:

```go
package tokens

import (
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func TestPlanRotationOnAFreshLibrary(t *testing.T) {
	today := boulevard.NewDate(2026, time.August, 22)
	got := PlanRotation(nil, today)
	if got.StartIndex != 1 {
		t.Errorf("StartIndex = %d, want 1", got.StartIndex)
	}
	if !got.Start.Equal(today) {
		t.Errorf("Start = %s, want %s", got.Start, today)
	}
}

// Mid-booklet: the active card survives and the next booklet begins the day
// after it ends. Indices continue from the next multiple of twelve so that
// booklet = (i-1)/12+1 and card = (i-1)%12+1 stay exact even though the
// discarded pending cards leave a gap.
func TestPlanRotationMidBooklet(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 1, ValidFrom: boulevard.NewDate(2026, time.August, 14), ValidUntil: boulevard.NewDate(2026, time.August, 31), State: boulevard.TokenExpired},
		{PeriodIndex: 2, ValidFrom: boulevard.NewDate(2026, time.September, 1), ValidUntil: boulevard.NewDate(2026, time.September, 30), State: boulevard.TokenActive},
		{PeriodIndex: 3, ValidFrom: boulevard.NewDate(2026, time.October, 1), ValidUntil: boulevard.NewDate(2026, time.October, 31), State: boulevard.TokenPending},
	}
	got := PlanRotation(toks, boulevard.NewDate(2026, time.September, 20))
	if got.StartIndex != 13 {
		t.Errorf("StartIndex = %d, want 13", got.StartIndex)
	}
	if want := boulevard.NewDate(2026, time.October, 1); !got.Start.Equal(want) {
		t.Errorf("Start = %s, want %s", got.Start, want)
	}
}

func TestPlanRotationTwiceKeepsTheArithmeticExact(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 24, ValidFrom: boulevard.NewDate(2027, time.August, 1), ValidUntil: boulevard.NewDate(2027, time.August, 31), State: boulevard.TokenActive},
	}
	got := PlanRotation(toks, boulevard.NewDate(2027, time.August, 15))
	if got.StartIndex != 25 {
		t.Errorf("StartIndex = %d, want 25", got.StartIndex)
	}
	if (got.StartIndex-1)/PeriodCount+1 != 3 {
		t.Errorf("booklet number = %d, want 3", (got.StartIndex-1)/PeriodCount+1)
	}
}

// Past month 12 there is nothing pending left; the new twelve simply follow
// the current card.
func TestPlanRotationPastTheLastCard(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 12, ValidFrom: boulevard.NewDate(2027, time.July, 1), ValidUntil: boulevard.NewDate(2027, time.July, 31), State: boulevard.TokenActive},
	}
	got := PlanRotation(toks, boulevard.NewDate(2027, time.July, 20))
	if got.StartIndex != 13 {
		t.Errorf("StartIndex = %d, want 13", got.StartIndex)
	}
	if want := boulevard.NewDate(2027, time.August, 1); !got.Start.Equal(want) {
		t.Errorf("Start = %s, want %s", got.Start, want)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/tokens`
Expected: FAIL to compile — `undefined: PlanRotation`.

- [ ] **Step 3: Write the planner**

Create `internal/tokens/rotate.go`:

```go
package tokens

import "github.com/gumptionthomas/boulevard/internal/boulevard"

// Rotation says where the next booklet begins: which period_index its first
// card takes, and the date that card becomes valid.
type Rotation struct {
	StartIndex int
	Start      boulevard.Date
}

// PlanRotation computes the next booklet's starting point from what the
// library already holds. Pure: no clock, no database.
//
// Indices continue rather than restarting. tokens carries
// UNIQUE (library_id, period_index), and rotation mints twelve new cards
// while the active card still occupies one of 1..12 — so reusing those
// numbers is not available. The first rotation produces 13..24, the second
// 25..36, and two derived values stay exact:
//
//	booklet = (period_index - 1) / PeriodCount + 1
//	card    = (period_index - 1) % PeriodCount + 1
//
// Starting on a multiple-of-twelve boundary rather than simply after the
// highest index in use is what keeps that arithmetic true when a rotation
// discards a partly-used booklet's pending cards.
//
// The active card is not touched: an action taken at a keyboard must never
// make the box stop working. Killing the live card stays a separate,
// deliberate act (revoke).
func PlanRotation(existing []boulevard.Token, today boulevard.Date) Rotation {
	maxIndex := 0
	start := today
	for _, tok := range existing {
		if tok.PeriodIndex > maxIndex {
			maxIndex = tok.PeriodIndex
		}
		if tok.State == boulevard.TokenActive {
			start = tok.ValidUntil.NextDay()
		}
	}
	booklets := (maxIndex + PeriodCount - 1) / PeriodCount
	return Rotation{StartIndex: booklets*PeriodCount + 1, Start: start}
}
```

- [ ] **Step 4: Run the planner tests**

Run: `go test ./internal/tokens`
Expected: PASS.

- [ ] **Step 5: Write the failing store test**

Append to `internal/store/token_state_test.go`:

```go
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
```

Add `"crypto/rand"` and `"github.com/gumptionthomas/boulevard/internal/tokens"` to that file's imports.

- [ ] **Step 6: Run it and watch it fail**

Run: `go test -run TestRotate ./internal/store`
Expected: FAIL to compile — `s.RotatePendingTokens undefined`.

- [ ] **Step 7: Implement the store side**

Append to `internal/store/token.go`:

```go
// RotatePendingTokens discards every unprinted card and inserts a fresh
// booklet in one transaction. The caller mints the secrets and the ids and
// computes the periods (tokens.PlanRotation plus tokens.Periods) — the same
// split InsertTokens already uses, which keeps randomness injectable and
// the period arithmetic unit-testable without a database.
//
// Only "pending" rows are deleted. A pending card has never granted a
// session: tokens.Validate answers NotYet before its window opens, and the
// first scan inside the window is what makes it active. So nothing in
// `sessions` references the rows this removes, and the foreign key from
// sessions.token_id has nothing to complain about.
func (s *Store) RotatePendingTokens(ctx context.Context, id boulevard.LibraryID, toks []boulevard.Token) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rotate: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tokens WHERE library_id = ? AND state = ?`,
		string(id), string(boulevard.TokenPending)); err != nil {
		return fmt.Errorf("discard pending tokens: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO tokens (id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`)
	if err != nil {
		return fmt.Errorf("prepare rotate insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, tok := range toks {
		if tok.LibraryID != id {
			return fmt.Errorf("token %d belongs to library %q, not %q", tok.PeriodIndex, tok.LibraryID, id)
		}
		if _, err := stmt.ExecContext(ctx,
			tok.ID, string(id), tok.Secret, tok.PeriodIndex,
			tok.ValidFrom.String(), tok.ValidUntil.String(), string(tok.State), now,
		); err != nil {
			return fmt.Errorf("insert rotated token for period %d: %w", tok.PeriodIndex, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rotate: %w", err)
	}
	return nil
}
```

- [ ] **Step 8: Run everything, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/tokens/rotate.go internal/tokens/rotate_test.go internal/store/token.go internal/store/token_state_test.go
git commit -m "feat: rotate a library onto a fresh booklet"
```

---

### Task 4: Export a library as a runnable database file

**Files:**
- Create: `internal/store/export.go`
- Create: `internal/store/export_test.go`

**Interfaces:**
- Consumes: `Open(path string) (*Store, error)` — applies migrations and restricts the file to `0600`; `LibraryByID(ctx, id) (boulevard.Library, error)`.
- Produces: `func (s *Store) CopyLibraryTo(ctx context.Context, id boulevard.LibraryID, path string) error`

- [ ] **Step 1: Write the failing test**

Create `internal/store/export_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// The marquee test: an export is only "a file copy, not a format" if a
// plain Open can read it back with no translation.
func TestExportRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)

	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if err := s.RecordScan(ctx, lib.ID, toks[0], now); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	shelved := insertExportItem(t, s, lib, "on the shelf")
	if err := s.ApproveItem(ctx, lib.ID, shelved, now, lib.Slots, lib.DefaultCopies); err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	waiting := insertExportItem(t, s, lib, "still waiting")

	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := s.CopyLibraryTo(ctx, lib.ID, out); err != nil {
		t.Fatalf("CopyLibraryTo: %v", err)
	}

	dst, err := Open(out)
	if err != nil {
		t.Fatalf("Open the export: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	got, err := dst.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatalf("LibraryBySlug on the export: %v", err)
	}
	if got.Name != lib.Name || got.BaseURL != lib.BaseURL {
		t.Errorf("library = %q/%q, want %q/%q", got.Name, got.BaseURL, lib.Name, lib.BaseURL)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat the export: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("export mode = %04o, want 0600 — it holds every card's secret", perm)
	}

	gotToks, err := dst.TokensForLibrary(ctx, got.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary on the export: %v", err)
	}
	if len(gotToks) != len(toks) {
		t.Errorf("%d tokens in the export, want %d", len(gotToks), len(toks))
	}
	// The copy must be runnable, which means the secrets travel intact.
	if gotToks[0].Secret != toks[0].Secret {
		t.Error("token secrets did not survive the export")
	}
	if gotToks[0].State != boulevard.TokenActive {
		t.Errorf("token state = %q, want the scanned card still active", gotToks[0].State)
	}

	shelf, err := dst.ShelvedItems(ctx, got.ID)
	if err != nil {
		t.Fatalf("ShelvedItems on the export: %v", err)
	}
	if len(shelf) != 1 {
		t.Fatalf("%d shelved items in the export, want 1", len(shelf))
	}
	pending, err := dst.PendingItems(ctx, got.ID)
	if err != nil {
		t.Fatalf("PendingItems on the export: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != waiting {
		t.Errorf("pending items = %v, want just %s", pending, waiting)
	}
}

// Live credentials and the take record must not travel. Copying sessions
// would hand working credentials to whoever holds the file; copying
// session_takes would move exactly the durable "who took what" record that
// table's swept-with-the-session design exists to avoid being.
func TestExportLeavesEverySessionTableEmpty(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if err := s.RecordScan(ctx, lib.ID, toks[0], now); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	if _, err := s.CreateSession(ctx, lib.ID, toks[0].ID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.SetStewardKeyHash(ctx, lib.ID, "deadbeef"); err != nil {
		t.Fatalf("SetStewardKeyHash: %v", err)
	}
	if _, err := s.CreateStewardSession(ctx, lib.ID, now, now.Add(30*24*time.Hour)); err != nil {
		t.Fatalf("CreateStewardSession: %v", err)
	}

	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := s.CopyLibraryTo(ctx, lib.ID, out); err != nil {
		t.Fatalf("CopyLibraryTo: %v", err)
	}
	dst, err := Open(out)
	if err != nil {
		t.Fatalf("Open the export: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	for _, table := range []string{"sessions", "steward_sessions", "session_takes"} {
		var n int
		if err := dst.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows in the export, want 0", table, n)
		}
	}

	// The steward key does travel: §10's story is a host handing out twelve
	// files, and each goes to the steward whose key that hash already is.
	set, err := dst.StewardKeyIsSet(ctx, lib.ID)
	if err != nil {
		t.Fatalf("StewardKeyIsSet: %v", err)
	}
	if !set {
		t.Error("steward key hash did not travel; the recipient is locked out of their own box")
	}
}

func insertExportItem(t *testing.T, s *Store, lib boulevard.Library, note string) string {
	t.Helper()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: "text",
		Payload: note, Note: note, State: boulevard.ItemPending,
	}
	if err := s.CreateItem(context.Background(), it, time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return id
}
```

**Before writing the implementation, reconcile this test with the real API.** Six calls above are written from the plan's reading of the store and may differ in argument order or return values: `CreateItem`, `ApproveItem`, `CreateSession`, `CreateStewardSession`, `StewardKeyIsSet`, and `boulevard.Item`'s field names. Check each signature in `internal/store/` and `internal/boulevard/` and adjust the **test** to match what is there. Do not invent or widen a store method to make the test compile — if a fixture is awkward to build, that is a fact about the test, not a reason to change production code. `openTemp`, `seededLibrary`, `stateOf` and `makeTokens` were verified and are correct as written.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test -run TestExport ./internal/store`
Expected: FAIL to compile — `s.CopyLibraryTo undefined`.

- [ ] **Step 3: Implement the copy**

Create `internal/store/export.go`:

```go
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// exportedTables are copied in dependency order: items and tokens both
// carry a foreign key to libraries, and _pragma=foreign_keys(1) is on.
//
// What is missing matters as much as what is here. sessions,
// steward_sessions and session_takes are deliberately not copied. Durable
// library state travels; ephemeral presence state does not. Copying the two
// session tables would hand working credentials to whoever holds the file,
// and copying session_takes would move exactly the durable "who took what"
// record that table's swept-with-the-session design exists to avoid being —
// the same reasoning that keeps a session id out of the request log and out
// of a left item.
//
// libraries.steward_key_hash does travel: DESIGN.md §10's story is a host
// who loses interest handing out twelve files and dissolving cleanly, and
// each of those files goes to the steward whose key that hash already is.
var exportedTables = []struct {
	name    string
	columns string
	where   string
}{
	{"libraries", "id, slug, name, location_label, base_url, created_at, slots, max_age_days, default_copies, approval_required, steward_contact, steward_key_hash", "id = ?"},
	{"tokens", "id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at, created_at", "library_id = ?"},
	{"items", "id, library_id, type, payload, note, attribution, copies_total, copies_left, state, pinned, views, takes, left_at, shelved_at, created_at, shed_at, shed_reason", "library_id = ?"},
}

// CopyLibraryTo writes one library to its own SQLite file at path.
//
// The target is created through Open, which applies the same migrations and
// restricts the file to 0600 — the export carries every card's secret, and
// a secret is the shelf's write credential (DESIGN.md §4). Same schema in,
// same schema out: `boulevard serve --db <path>` runs the result unchanged,
// which is what makes ejection a file copy rather than a format (§10), and
// makes v2's move to one-database-per-library a no-op rather than a
// migration.
//
// Rows are read fully into memory before the target is written. The source
// runs with SetMaxOpenConns(1), so holding a *sql.Rows open across another
// query on it would deadlock rather than error. Reading everything first is
// safe here because both the shelf (capped at `slots`) and the booklet
// (always twelve cards) are bounded by design.
func (s *Store) CopyLibraryTo(ctx context.Context, id boulevard.LibraryID, path string) error {
	if _, err := s.LibraryByID(ctx, id); err != nil {
		return err
	}

	dst, err := Open(path)
	if err != nil {
		return fmt.Errorf("create export at %q: %w", path, err)
	}
	defer dst.Close()

	tx, err := dst.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin export: %w", err)
	}
	defer tx.Rollback()

	for _, tbl := range exportedTables {
		rows, err := s.readTable(ctx, tbl.name, tbl.columns, tbl.where, string(id))
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		placeholders := "?" + strings.Repeat(", ?", len(rows[0])-1)
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO `+tbl.name+` (`+tbl.columns+`) VALUES (`+placeholders+`)`)
		if err != nil {
			return fmt.Errorf("prepare %s insert: %w", tbl.name, err)
		}
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, r...); err != nil {
				stmt.Close()
				return fmt.Errorf("copy %s row: %w", tbl.name, err)
			}
		}
		stmt.Close()
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit export: %w", err)
	}
	return nil
}

// readTable drains one table's matching rows into memory. Values come back
// as []any of whatever the driver produced, which is exactly what the
// matching INSERT wants — no per-table struct, and no column list to keep
// in sync in two places.
//
// It drains before returning rather than streaming. The source runs with
// SetMaxOpenConns(1), so holding a *sql.Rows open while the caller issues
// another query on the same handle would deadlock rather than error. Both
// the shelf (capped at `slots`) and the booklet (twelve cards) are bounded
// by design, so there is no unbounded read to worry about.
func (s *Store) readTable(ctx context.Context, name, columns, where, arg string) ([][]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM `+name+` WHERE `+where, arg)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns of %s: %w", name, err)
	}

	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan %s: %w", name, err)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", name, err)
	}
	return out, nil
}

```

The import block is `context`, `fmt`, `strings`, and `internal/boulevard`. There is no `database/sql` import: nothing in this file names a `sql.` type.

- [ ] **Step 4: Run the tests**

Run: `go test -run TestExport ./internal/store`
Expected: PASS.

- [ ] **Step 5: Run everything, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/store/export.go internal/store/export_test.go
git commit -m "feat: export a library as a runnable database file"
```

---

### Task 5: The `tokens`, `force-activate`, `extend` and `revoke` commands

**Files:**
- Create: `cmd/boulevard/tokens.go`
- Modify: `cmd/boulevard/main.go` (four dispatch cases, usage text)
- Test: `cmd/boulevard/tokens_test.go`

**Interfaces:**
- Consumes: `queueLibrary(dbPath, slug string) (*store.Store, boulevard.Library, int)` from `cmd/boulevard/queue.go` — resolves the library or prints the error and returns an exit code; `exitOK`, `exitUsage`, `exitIO` from `main.go`; the store methods from Tasks 2 and 3.
- Produces: `runTokens(args []string) int`, `runForceActivate(args []string) int`, `runExtend(args []string) int`, `runRevoke(args []string) int`.

- [ ] **Step 1: Read the existing pattern**

Read `cmd/boulevard/shed.go` end to end and `cmd/boulevard/queue.go`'s `queueLibrary`. Match their output shape exactly: two leading spaces, `  x  ` for errors on stderr, a blank line before and after a listing.

- [ ] **Step 2: Write the failing test**

The helpers are `cliStore(t, slugs ...string) (path string, s *store.Store, libs []boulevard.Library)` in `cmd/boulevard/queue_cli_test.go` and `captureStdout(t *testing.T, fn func()) string` in `cmd/boulevard/booklet_test.go` — same package, so both are in scope. `cliStore` creates libraries only; it inserts **no** tokens, so this file seeds its own.

Create `cmd/boulevard/tokens_test.go`:

```go
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
```

- [ ] **Step 3: Run and watch it fail**

Run: `go test ./cmd/boulevard`
Expected: FAIL to compile — `undefined: runTokens`.

- [ ] **Step 4: Implement the four commands**

Create `cmd/boulevard/tokens.go` with:

```go
// runTokens lists the booklet: one line per card, with the period, the
// dates, the state, and whether it has been seen.
//
// Secrets are never printed. A secret is the shelf's write credential
// (DESIGN.md §4), and this output is the thing a steward pastes into a
// support thread. To put the secrets in front of someone, print the
// booklet — that artifact is meant to carry them and is written 0600.
func runTokens(args []string) int
func runForceActivate(args []string) int
func runExtend(args []string) int
func runRevoke(args []string) int
```

Each follows `runShed`'s shape: a `flag.NewFlagSet` with `--db` and `--slug`, then `queueLibrary`, then the store call. `force-activate` and `revoke` take one positional argument, the period number, parsed with `strconv.Atoi`; a non-numeric argument exits `exitUsage` with `  x  "%s" is not a card number. Cards are numbered 1-12 in the booklet.` `extend` takes the period number too, and refuses with `ErrNotActive` naming which card *is* active.

Map errors to exits: `store.ErrNotFound` → `exitUsage` with `  x  no card numbered %d`; `store.ErrNotPending`, `store.ErrNotActive`, `store.ErrAlreadyRevoked` → `exitUsage` naming the state found; anything else → `exitIO`.

For `runTokens`, compute each card's booklet and position for display:

```go
	booklet := (tok.PeriodIndex-1)/tokens.PeriodCount + 1
	card := (tok.PeriodIndex-1)%tokens.PeriodCount + 1
```

Print `BOOKLET 2 · CARD 1` when `booklet > 1`, and just `CARD 1` otherwise, so an unrotated library reads exactly as it does today.

- [ ] **Step 5: Wire the dispatch**

In `cmd/boulevard/main.go`, add four cases beside the existing ones and add the commands to the usage text:

```go
	case "tokens":
		os.Exit(runTokens(os.Args[2:]))
	case "force-activate":
		os.Exit(runForceActivate(os.Args[2:]))
	case "extend":
		os.Exit(runExtend(os.Args[2:]))
	case "revoke":
		os.Exit(runRevoke(os.Args[2:]))
```

- [ ] **Step 6: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add cmd/boulevard/tokens.go cmd/boulevard/tokens_test.go cmd/boulevard/main.go
git commit -m "feat: tokens, force-activate, extend and revoke on the CLI"
```

---

### Task 6: The `export` command

**Files:**
- Create: `cmd/boulevard/export.go`
- Modify: `cmd/boulevard/main.go` (one dispatch case, usage text)
- Test: `cmd/boulevard/export_test.go`

**Interfaces:**
- Consumes: `queueLibrary`, `store.CopyLibraryTo` from Task 4.
- Produces: `runExport(args []string) int`.

- [ ] **Step 1: Write the failing test**

Create `cmd/boulevard/export_test.go` using the same fixture helper as Task 5:

```go
func TestExportWritesARunnableFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fairview.db")
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out}); code != exitOK {
		t.Fatalf("exit = %d, want %d", code, exitOK)
	}
	s, err := store.Open(out)
	if err != nil {
		t.Fatalf("the export does not open: %v", err)
	}
	defer s.Close()
	if _, err := s.LibraryBySlug(context.Background(), "fairview"); err != nil {
		t.Errorf("the export has no library: %v", err)
	}
}

func TestExportRefusesToOverwriteWithoutForce(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out}); code == exitOK {
		t.Error("overwrote an existing file without --force")
	}
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out, "--force"}); code != exitOK {
		t.Errorf("--force did not permit the overwrite, exit = %d", code)
	}
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test -run TestExport ./cmd/boulevard`
Expected: FAIL to compile — `undefined: runExport`.

- [ ] **Step 3: Implement**

Create `cmd/boulevard/export.go`. Flags: `--db`, `--slug`, `--out` (required), `--force`. Refuse an existing `--out` without `--force`, using `booklet.go`'s wording as the model:

```
  x  fairview.db already exists. Pass --force to overwrite it.
```

With `--force`, remove the existing file before calling `CopyLibraryTo` — `Open` would otherwise migrate and append into it, producing a file with two libraries' worth of rows. On success:

```

  fairview.db  (1 library, 14 items, 12 tokens)

  It holds every card's secret. Keep it as you keep boulevard.db.
  Run it as-is:  boulevard serve --db fairview.db

```

- [ ] **Step 4: Wire the dispatch**

```go
	case "export":
		os.Exit(runExport(os.Args[2:]))
```

- [ ] **Step 5: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add cmd/boulevard/export.go cmd/boulevard/export_test.go cmd/boulevard/main.go
git commit -m "feat: export on the CLI"
```

---

### Task 7: `booklet --rotate`

**Files:**
- Modify: `cmd/boulevard/booklet.go`
- Test: `cmd/boulevard/booklet_test.go`

**Interfaces:**
- Consumes: `tokens.PlanRotation`, `tokens.Periods`, `store.RotatePendingTokens`, `store.TokensForLibrary`.
- Produces: no new exported names; `runBooklet` gains a `--rotate` flag.

- [ ] **Step 1: Read `runBooklet` end to end**

It already has three modes — create a library, reprint an existing one unchanged, and refuse. `--rotate` is a fourth. Note how `dbPeek` reports what the database holds and how the confirmation prompt works; `--rotate` must go through the same prompt, because it discards secrets.

- [ ] **Step 2: Write the failing test**

Add to `cmd/boulevard/booklet_test.go`:

Read `booklet_test.go` first for how it supplies the confirmation prompt's input and where it points `--out`; match that, and reuse `seedTokens` from `tokens_test.go` (same package).

```go
// --rotate mints a new booklet and leaves the card in the door alone.
func TestBookletRotateKeepsTheActiveCard(t *testing.T) {
	ctx := context.Background()
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	seedTokens(t, s, lib)
	if err := s.ForceActivateToken(ctx, lib.ID, 1, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatal(err)
	}
	before, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var activeSecret string
	for _, tok := range before {
		if tok.State == boulevard.TokenActive {
			activeSecret = tok.Secret
		}
	}

	out := filepath.Join(t.TempDir(), "booklet.pdf")
	if code := runBooklet([]string{
		"--db", path, "--slug", "fairview", "--rotate", "--out", out,
		"--yes", "--skip-dns",
	}); code != exitOK {
		t.Fatalf("booklet --rotate exit = %d, want %d", code, exitOK)
	}

	after, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != tokens.PeriodCount+1 {
		t.Fatalf("%d tokens after rotate, want %d", len(after), tokens.PeriodCount+1)
	}
	kept, fresh := false, 0
	for _, tok := range after {
		if tok.Secret == activeSecret && tok.State == boulevard.TokenActive {
			kept = true
		}
		if tok.PeriodIndex >= 13 && tok.PeriodIndex <= 24 {
			fresh++
		}
	}
	if !kept {
		t.Error("the card in the door did not survive --rotate")
	}
	if fresh != tokens.PeriodCount {
		t.Errorf("%d cards at 13..24, want %d", fresh, tokens.PeriodCount)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no PDF written: %v", err)
	}
}

func TestBookletRotateRefusesWithoutAnExistingLibrary(t *testing.T) {
	path, _, _ := cliStore(t)
	out := filepath.Join(t.TempDir(), "booklet.pdf")
	code := runBooklet([]string{
		"--db", path, "--slug", "nowhere", "--rotate", "--out", out,
		"--yes", "--skip-dns",
	})
	if code == exitOK {
		t.Error("--rotate created a library instead of refusing")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("--rotate wrote a PDF for a library that does not exist")
	}
}
```

`--yes` and `--skip-dns` are how every existing `runBooklet` test gets past the confirmation prompt and the DNS check; there is no wrapper helper, and none should be added.

- [ ] **Step 3: Run and watch them fail**

Run: `go test -run TestBookletRotate ./cmd/boulevard`
Expected: FAIL.

- [ ] **Step 4: Implement**

Add `--rotate` to the flag set (`fs.BoolVar(&o.rotate, "rotate", false, "mint a new booklet for an existing library")`). When set:

0. **`--name`, `--location` and `--base-url` stop being required.** They are required today because the create path needs them; rotation targets a library that already has all three, and demanding them again invites a steward to retype a base URL that is printed on a mounted sign. Take every one of those values from the stored library, and if any of the three *is* passed, refuse with `  x  --rotate mints new cards for an existing library; it cannot change its %s.` rather than silently ignoring it. Add a test for that refusal.

1. The library must already exist; otherwise `  x  no library %q to rotate. Run \`boulevard booklet\` without --rotate to create one.` and `exitUsage`.
2. Read `TokensForLibrary`, call `tokens.PlanRotation(existing, boulevard.DateFromTime(time.Now()))`.
3. Build twelve tokens exactly as the create path does — `tokens.Periods(rot.Start, tokens.PeriodCount)`, `tokens.NewSecret(rand.Reader)`, `boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)` — with `PeriodIndex: rot.StartIndex + p.Index - 1`.
4. Warn before the existing confirmation prompt:

```

  Rotating bryant-ave-boulevard onto a new booklet.
    The card currently in the door keeps working.
    The 11 unprinted cards are replaced and their secrets discarded.

```

Say `Every card is new.` instead of the second and third lines when no card is active.

5. Call `RotatePendingTokens`, then render the PDF from the **new twelve only** — `BuildPlan` requires exactly `tokens.PeriodCount` tokens, so filter `TokensForLibrary`'s result to those with `PeriodIndex >= rot.StartIndex` before building the plan.

- [ ] **Step 5: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add cmd/boulevard/booklet.go cmd/boulevard/booklet_test.go
git commit -m "feat: booklet --rotate mints the next twelve cards"
```

---

### Task 8: The tokens page

**Files:**
- Create: `internal/web/steward_tokens.go`
- Create: `internal/web/templates/steward-tokens.html`
- Modify: `internal/web/steward.go` (`stewardData` gains one field)
- Modify: `internal/web/server.go` (`pageTemplates`)
- Modify: `internal/web/routes.go` (one route)
- Modify: `internal/web/templates/steward-hub.html` (one row)
- Test: `internal/web/steward_tokens_test.go`

**Interfaces:**
- Consumes: `s.requireSteward(w, r) (boulevard.Library, boulevard.StewardSession, bool)`; `s.render(w, status, name string, data any)`; `stewardPath(lib) string`; `stewardData`; `okMessages` in `steward_items.go`; `s.store.TokensForLibrary(ctx, id)`.
- Produces: `type tokenRow`, `func (s *Server) handleStewardTokens(w http.ResponseWriter, r *http.Request)`, `func (s *Server) renderStewardTokens(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/web/steward_tokens_test.go`:

```go
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
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test -run TestTokensPage ./internal/web`
Expected: FAIL — 404 on a route that does not exist yet, and a compile error if the helper names are wrong.

- [ ] **Step 3: Add the view type and handler**

Create `internal/web/steward_tokens.go`:

```go
package web

import (
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// tokenRow is one card as the page shows it.
//
// Secret is deliberately absent. A secret is the shelf's write credential
// (DESIGN.md §4), and this page is the one a steward is most likely to
// screenshot or hand to someone standing next to them. The way to put
// secrets in front of a person is the booklet, which is an artifact meant
// to carry them.
//
// Booklet and Card are derived rather than stored: rotation makes
// period_index monotonic (13..24, then 25..36), so the number printed on a
// card is its position within its own booklet.
type tokenRow struct {
	Period   int
	Booklet  int
	Card     int
	Month    string
	Year     int
	From     boulevard.Date
	Until    boulevard.Date
	State    boulevard.TokenState
	Seen     bool
	Active   bool
	CanForce bool
	CanExtend bool
	CanRevoke bool
}

func (s *Server) handleStewardTokens(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardTokens(w, r, lib, http.StatusOK, okMessages[r.URL.Query().Get("ok")], "")
}

func (s *Server) renderStewardTokens(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string) {
	toks, err := s.store.TokensForLibrary(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	rows := make([]tokenRow, 0, len(toks))
	for _, tok := range toks {
		active := tok.State == boulevard.TokenActive
		rows = append(rows, tokenRow{
			Period:  tok.PeriodIndex,
			Booklet: (tok.PeriodIndex-1)/tokens.PeriodCount + 1,
			Card:    (tok.PeriodIndex-1)%tokens.PeriodCount + 1,
			Month:   tok.ValidFrom.MonthName(),
			Year:    tok.ValidFrom.Year,
			From:    tok.ValidFrom,
			Until:   tok.ValidUntil,
			State:   tok.State,
			Seen:    tok.FirstSeenAt != nil,
			Active:  active,
			// Actions are shown only where they are legal, so the page never
			// offers a tap that can only fail. This is a different reason
			// from a pinned item hiding its take control: there the action
			// does not exist for anyone, here it is this card's state that
			// makes it illegal.
			CanForce:  tok.State == boulevard.TokenPending,
			CanExtend: active,
			CanRevoke: tok.State != boulevard.TokenRevoked,
		})
	}

	s.render(w, status, "steward-tokens.html", stewardData{
		Title:       "Tokens — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Notice:      notice,
		Error:       errMsg,
		TokenRows:   rows,
		Now:         s.now(),
	})
}
```

Add `TokenRows []tokenRow` to `stewardData` in `internal/web/steward.go`, beside `Items`.

- [ ] **Step 4: Write the template**

Create `internal/web/templates/steward-tokens.html`, modelled on `steward-shed.html`:

```html
{{define "steward-tokens.html"}}{{template "layout" .}}{{end}}
{{define "body"}}
<main>
  <div class="site">{{.LibraryName}}</div>
  <h1>Tokens</h1>
  {{if .Error}}<p class="err">{{.Error}}</p>{{end}}
  {{if .Notice}}<p class="dim">{{.Notice}}</p>{{end}}
  {{range .TokenRows}}
    <div class="item">
      <p class="note">
        {{.Month}} {{.Year}}
        {{if .Active}}<span class="tag">in the door</span>{{end}}
      </p>
      <p class="src">
        {{if gt .Booklet 1}}Booklet {{.Booklet}} · {{end}}Card {{.Card}} of 12 ·
        {{.From}} – {{.Until}} · {{.State}}
      </p>
      {{if .Seen}}<p class="dim">seen</p>{{else}}<p class="dim">never scanned</p>{{end}}
      {{if .CanForce}}
        <form method="post" action="{{$.StewardURL}}tokens/{{.Period}}/force-activate">
          <button type="submit">Put this card in the door</button>
        </form>
      {{end}}
      {{if .CanExtend}}
        <form method="post" action="{{$.StewardURL}}tokens/{{.Period}}/extend">
          <button type="submit">Give it another month</button>
        </form>
      {{end}}
      {{if .CanRevoke}}
        <form method="post" action="{{$.StewardURL}}tokens/{{.Period}}/revoke">
          <button class="linkish" type="submit">Revoke</button>
        </form>
      {{end}}
    </div>
  {{end}}
  <a class="btn" href="{{.StewardURL}}">Back to the hub</a>
</main>
{{end}}
```

Task 10 adds the booklet download and the rotate control to the bottom of this page.

- [ ] **Step 5: Register the template and the route**

`internal/web/server.go`, in `pageTemplates`: add `"steward-tokens.html"`.

`internal/web/routes.go`, beside the other steward GETs:

```go
	mux.HandleFunc("GET /b/{slug}/steward/tokens", s.handleStewardTokens)
```

`internal/web/templates/steward-hub.html`: add a row linking to `{{.StewardURL}}tokens`, matching the existing rows' markup.

- [ ] **Step 6: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/web/steward_tokens.go internal/web/templates/steward-tokens.html internal/web/templates/steward-hub.html internal/web/steward.go internal/web/server.go internal/web/routes.go internal/web/steward_tokens_test.go
git commit -m "feat: the steward's tokens page"
```

---

### Task 9: The four token mutations on the desk

**Files:**
- Modify: `internal/web/steward_tokens.go` (four handlers)
- Modify: `internal/web/steward_items.go` (`okMessages` gains four codes)
- Modify: `internal/web/routes.go` (four routes)
- Test: `internal/web/steward_tokens_test.go`

**Interfaces:**
- Consumes: `withOK(path, code string) string` from `steward_items.go`; `store.ErrNotFound`, `store.ErrNotPending`, `store.ErrNotActive`, `store.ErrAlreadyRevoked`; the store methods from Tasks 2 and 3; `loginAsSteward(t, h, lib, key) *http.Cookie` and `postForm(t, h, path, form, cookie)` from `steward_test.go`.
- Produces: `handleStewardForceActivate`, `handleStewardExtend`, `handleStewardRevoke`, `handleStewardRotate`.

- [ ] **Step 1: Add the confirmation codes**

In `internal/web/steward_items.go`, extend `okMessages`:

```go
	"activated": "That card is now the one in the door. Put it there.",
	"extended":  "Extended. The card in the door works for another month.",
	"revoked":   "Revoked. That card no longer works — swap it before anyone tries it.",
	"rotated":   "A new booklet is ready. Print it before the card in the door runs out.",
```

- [ ] **Step 2: Write the failing tests**

Add to `internal/web/steward_tokens_test.go`:

```go
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

	if rec := get(t, h, "/s/"+toks[0].Secret); rec.Code != http.StatusSeeOther {
		t.Fatalf("before revoke: scan status = %d, want a session", rec.Code)
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
```

- [ ] **Step 3: Run and watch them fail**

Run: `go test -run 'TestForceActivateFrom|TestRevokeFromTheDesk|TestRotateFromTheDesk|TestTokenMutations' ./internal/web`
Expected: FAIL.

- [ ] **Step 4: Implement the handlers**

Append to `internal/web/steward_tokens.go`. Each follows `handleStewardPin`'s shape exactly: `requireSteward`, read the path value, call the store, switch on the sentinels, and either re-render with a status and a message or redirect through `withOK`.

Parse the period with `strconv.Atoi(r.PathValue("period"))`; a non-numeric value renders the page with `http.StatusNotFound` and the message `That card number isn't valid.`

Refusal messages, each naming what was found:

- `ErrNotPending` → `409` — `That card is already ` + state + `. Only an unused card can be put in the door.`
- `ErrNotActive` → `409` — `Only the card in the door can be extended.`
- `ErrAlreadyRevoked` → `409` — `That card is already revoked.`
- `ErrNotFound` → `404` — `That card number isn't valid.`
- anything else → `noStore(w)` and a 500, matching every other handler.

For rotate, build the twelve tokens exactly as Task 7's CLI path does — `TokensForLibrary`, `tokens.PlanRotation(existing, boulevard.DateFromTime(s.now()))`, then `tokens.Periods(rot.Start, tokens.PeriodCount)` with `PeriodIndex: rot.StartIndex + p.Index - 1` — and use `crypto/rand`'s `rand.Reader` for secrets and ids.

- [ ] **Step 5: Register the routes**

In `internal/web/routes.go`, beside the other steward POSTs:

```go
	mux.HandleFunc("POST /b/{slug}/steward/tokens/{period}/force-activate", s.handleStewardForceActivate)
	mux.HandleFunc("POST /b/{slug}/steward/tokens/{period}/extend", s.handleStewardExtend)
	mux.HandleFunc("POST /b/{slug}/steward/tokens/{period}/revoke", s.handleStewardRevoke)
	mux.HandleFunc("POST /b/{slug}/steward/tokens/rotate", s.handleStewardRotate)
```

- [ ] **Step 6: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/web/steward_tokens.go internal/web/steward_items.go internal/web/routes.go internal/web/steward_tokens_test.go
git commit -m "feat: force-activate, extend, revoke and rotate from the desk"
```

---

### Task 10: The two downloads

**Files:**
- Modify: `internal/web/steward_tokens.go` (the PDF handler, and the rotate/download controls in the template)
- Create: `internal/web/steward_export.go`
- Create: `internal/web/templates/steward-export.html`
- Modify: `internal/web/templates/steward-tokens.html`, `internal/web/templates/steward-hub.html`, `internal/web/server.go`, `internal/web/routes.go`
- Test: `internal/web/steward_downloads_test.go`

**Interfaces:**
- Consumes: `booklet.BuildPlan(booklet.Input{Library, Tokens, SourceURL, BuildLine}) (booklet.Plan, error)` — **requires exactly `tokens.PeriodCount` tokens**; `booklet.Renderer{CreationDate time.Time}.Render(plan) ([]byte, error)`; `store.CopyLibraryTo`; `version.RepoURL`, `version.Version`, `version.Commit`.
- Produces: `handleStewardBookletPDF`, `handleStewardExport`, `handleStewardExportDownload`.

- [ ] **Step 1: Write the failing tests**

Create `internal/web/steward_downloads_test.go`:

```go
func TestBookletDownloadServesAPDF(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/booklet.pdf", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	if got := rec.Body.Bytes(); len(got) < 4 || string(got[:4]) != "%PDF" {
		t.Error("the body is not a PDF")
	}
}

// The round trip, over HTTP: the export is only "a file copy, not a format"
// if a plain store.Open reads back what the desk sent.
func TestExportDownloadOpensAsADatabase(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/export.db", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	path := filepath.Join(t.TempDir(), "downloaded.db")
	if err := os.WriteFile(path, rec.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dst, err := store.Open(path)
	if err != nil {
		t.Fatalf("the downloaded file does not open as a database: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	got, err := dst.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatalf("the download has no library: %v", err)
	}
	if got.Name != lib.Name {
		t.Errorf("library name = %q, want %q", got.Name, lib.Name)
	}
}

func TestDownloadPagesCarryThePlaintextWarning(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	for _, path := range []string{"/steward/tokens", "/steward/export"} {
		rec := getWithCookie(t, h, "/b/"+lib.Slug+path, c)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not encrypted") {
			t.Errorf("%s does not warn that the connection is not encrypted", path)
		}
	}
}

func TestDownloadsRequireASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, time.Now).Handler()

	for _, path := range []string{"/steward/booklet.pdf", "/steward/export", "/steward/export.db"} {
		if rec := get(t, h, "/b/"+lib.Slug+path); rec.Code != http.StatusSeeOther {
			t.Errorf("%s without a session = %d, want 303", path, rec.Code)
		}
	}

	// And nothing at all before a key exists.
	bare := testStore(t)
	other := addLibrary(t, bare, "unarmed")
	bareH := New(bare, time.Now).Handler()
	for _, path := range []string{"/steward/booklet.pdf", "/steward/export", "/steward/export.db"} {
		if rec := get(t, bareH, "/b/"+other.Slug+path); rec.Code != http.StatusNotFound {
			t.Errorf("%s with no steward key = %d, want 404", path, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test -run 'TestBookletDownload|TestExportDownload|TestDownloadPages|TestDownloadsRequire' ./internal/web`
Expected: FAIL.

- [ ] **Step 3: Implement the PDF handler**

In `internal/web/steward_tokens.go`:

```go
// handleStewardBookletPDF renders the current booklet and sends it.
//
// The twelve cards are the highest-numbered booklet the library holds:
// rotation makes period_index monotonic, and BuildPlan requires exactly
// twelve tokens. Reprinting an older booklet is not offered — its cards are
// either in the door already or discarded.
//
// This response carries every card's secret across the network, and serve
// terminates no TLS. The page linking here says so; withholding the
// capability would take away the thing this milestone exists to provide,
// and the steward key already crosses the same wire on every request.
//
// The work is bounded by design, which matters because SetMaxOpenConns(1)
// serialises every request through one connection: a booklet is always
// twelve cards, never more.
func (s *Server) handleStewardBookletPDF(w http.ResponseWriter, r *http.Request) {
```

Select the newest booklet: find `maxIndex` across `TokensForLibrary`, compute `start := ((maxIndex-1)/tokens.PeriodCount)*tokens.PeriodCount + 1`, and keep tokens with `PeriodIndex >= start`. If that yields fewer than `tokens.PeriodCount` tokens — possible when a rotation is half-applied or a card was deleted — render the tokens page with a 409 and `This booklet is incomplete. Rotate to mint a fresh twelve.` rather than serving a broken PDF.

Then:

```go
	pdf, err := booklet.Renderer{CreationDate: s.now()}.Render(plan)
	...
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lib.Slug+`-booklet.pdf"`)
	noStore(w)
	w.WriteHeader(http.StatusOK)
	w.Write(pdf)
```

- [ ] **Step 4: Implement the export page and download**

Create `internal/web/steward_export.go`. The page handler renders `steward-export.html`. The download handler writes to a temporary file, streams it, and removes it:

```go
// handleStewardExportDownload writes the library to a temporary file and
// sends it. CopyLibraryTo needs a path — it opens the target through
// store.Open so the export gets the same migrations and the same 0600 mode
// as boulevard.db, which a streaming writer could not provide.
func (s *Server) handleStewardExportDownload(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	dir, err := os.MkdirTemp("", "boulevard-export")
	if err != nil {
		noStore(w)
		http.Error(w, "cannot write the export", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, lib.Slug+".db")
	if err := s.store.CopyLibraryTo(r.Context(), lib.ID, path); err != nil {
		noStore(w)
		http.Error(w, "cannot write the export", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		noStore(w)
		http.Error(w, "cannot read the export", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lib.Slug+`.db"`)
	noStore(w)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}
```

`os.MkdirTemp` gives a `0700` directory, so the export never sits world-readable in `/tmp`. `defer os.RemoveAll(dir)` is what keeps a file full of secrets from accumulating there.

- [ ] **Step 5: Write the export template and the warning**

Create `internal/web/templates/steward-export.html` with the same structure as `steward-tokens.html`, containing the warning and one link to `{{.StewardURL}}export.db`. Use this copy on both pages, verbatim:

```html
<p class="err">
  This download holds every card's secret — the shelf's write credential.
  This connection is not encrypted, so anyone on this network can read it.
</p>
```

Add the booklet download link and the rotate form to the bottom of `steward-tokens.html`, above "Back to the hub":

```html
  <p class="err">
    This download holds every card's secret — the shelf's write credential.
    This connection is not encrypted, so anyone on this network can read it.
  </p>
  <a class="btn" href="{{.StewardURL}}booklet.pdf">Download the booklet</a>
  <form method="post" action="{{.StewardURL}}tokens/rotate">
    <button class="linkish" type="submit">Start a new booklet</button>
  </form>
```

Add an "Export" row to `steward-hub.html`.

- [ ] **Step 6: Register templates and routes**

`server.go`: add `"steward-export.html"` to `pageTemplates`.

`routes.go`:

```go
	mux.HandleFunc("GET /b/{slug}/steward/booklet.pdf", s.handleStewardBookletPDF)
	mux.HandleFunc("GET /b/{slug}/steward/export", s.handleStewardExport)
	mux.HandleFunc("GET /b/{slug}/steward/export.db", s.handleStewardExportDownload)
```

- [ ] **Step 7: Run, vet, format, commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/web/steward_tokens.go internal/web/steward_export.go internal/web/templates/ internal/web/server.go internal/web/routes.go internal/web/steward_downloads_test.go
git commit -m "feat: download the booklet and export the library from the desk"
```

---

### Task 11: Documentation and the acceptance checklist

**Files:**
- Modify: `DESIGN.md` (§6 steward surfaces, §8 install)
- Modify: `CLAUDE.md` (repository state, build order, CLI list, one invariant)
- Modify: `README.md`
- Create: `docs/token-acceptance.md`

**Interfaces:** none.

- [ ] **Step 1: Amend `DESIGN.md`**

In §6, replace the `Tokens:` and `Export:` bullets with what was built, and add a paragraph recording two decisions the spec settled:

- rotation leaves the active card alive, so no action taken at a keyboard makes the box stop working;
- `period_index` is monotonic across booklets, with `booklet` and `card` derived, because `UNIQUE (library_id, period_index)` makes reuse unavailable and a card's printed number is its position.

In §4, add one sentence to the **Steward overrides** paragraph recording that force-activate moves `valid_from`, and why: `Validate` reads `state` only for `revoked`.

In §8, add the five new commands to the CLI list.

- [ ] **Step 2: Amend `CLAUDE.md`**

Update the repository-state paragraph and the build order (4b done, 5 next). Add the five commands to the CLI block. Add one invariant:

```
- **A token operation that should change whether a card scans must move a date or set `revoked`.** `tokens.Validate` reads `state` for exactly one value — `revoked` — and everything else is the date window. This is why `ForceActivateToken` rewrites `valid_from` rather than only setting `state = active`: without it the command reports success and changes nothing a scanner can see, and its test asserts through `Validate` rather than the row so that failure cannot pass. `state` is bookkeeping for which card the steward thinks is in the door.
```

And one more:

```
- **`period_index` is monotonic across booklets, not 1–12.** Rotation mints 13–24, then 25–36, because `UNIQUE (library_id, period_index)` makes reuse impossible while the active card still holds one. A card's *printed* number is its position in the booklet being laid out — `placeCard` takes it as a parameter and never reads it from the token, or a rotated booklet prints "CARD 13 OF 12".
```

- [ ] **Step 3: Update `README.md`**

Add the new commands and one paragraph on export: it is a runnable database file, not a format, and `boulevard serve --db fairview.db` opens it unchanged.

- [ ] **Step 4: Write `docs/token-acceptance.md`**

Follow `docs/steward-acceptance.md`'s structure — a Set up section, a checklist of `- [ ]` items, and empty Run record / Still owed sections. Carry over its no-printer technique verbatim (the `pdftoppm` crop and `eog -f`), since this milestone's checks need a scannable card more than any before it. The checks, at minimum:

- [ ] Force-activate a card **more than 7 days early**, then scan that card and reach the shelf. Nothing else in this milestone proves the `valid_from` rewrite was necessary.
- [ ] Extend the active card; the tokens page shows the new end date and the next card's dates are unchanged.
- [ ] Revoke the active card, scan it, and get the generic failure page — visually identical to scanning a made-up secret.
- [ ] Rotate. The card in the door still scans. The tokens page shows twelve new pending cards numbered Card 1–12 of Booklet 2.
- [ ] Download the booklet on the phone. It has twelve cards, numbered 1–12, and the QR on the card matching the one in the door scans.
- [ ] Export from the phone, copy the file to the box, `boulevard serve --db` it, and load its shelf.
- [ ] The tokens page and the export page both warn that the connection is not encrypted.
- [ ] No page and no CLI command prints a token secret.

- [ ] **Step 5: Commit**

```bash
git add DESIGN.md CLAUDE.md README.md docs/token-acceptance.md
git commit -m "docs: milestone 4b, token management and export"
```

---

## Notes for the executor

**Three things earlier milestones got wrong in exactly this area.**

1. **Test helpers that do not exist.** Every helper named in this plan was verified against the repository on 22 August 2026: `openTemp`, `makeLibrary`, `seededLibrary`, `stateOf`, `makeTokens` (`internal/store`); `testStore`, `addLibrary`, `stewardServer`, `stewardServerWithItems`, `loginAsSteward`, `cookieNamed`, `get`, `getWithCookie`, `postForm` (`internal/web`); `cliStore`, `captureStdout`, `captureStderr`, `runBooklet` with `--yes --skip-dns` (`cmd/boulevard`). `seedTokens` (Task 5) and `seedWebTokens` (Task 8) are new and defined in this plan. If anything else is missing, it does not exist — write it in the test file rather than assuming.

2. **`*sql.Rows` held across another query is a hang, not an error**, because of `SetMaxOpenConns(1)`. `readTable` in Task 4 drains before returning for exactly this reason. Do not "optimise" it into a streaming copy.

3. **A test that asserts the wrong thing passes.** Task 2's force-activate test goes through `tokens.Validate`, not through the stored `state`. Milestone 3 shipped a Critical that ten passing tests missed because they asserted the error and not the state either side of it.

**Two further traps specific to this milestone.**

- `BuildPlan` refuses anything that is not exactly twelve tokens. After a rotation a library holds thirteen or more, so every path that renders a PDF must select one booklet's worth first.
- The golden-PDF fixture in `internal/booklet/testdata` must not change in Task 1. If it does, the card-numbering change is wrong — an unrotated booklet has `i+1 == PeriodIndex`, so the bytes are identical.
