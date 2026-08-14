package tokens

import (
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// A token valid for the whole of September 2026. With GraceDays = 7 its
// effective window runs 2026-08-25 through 2026-10-07 inclusive.
func septemberToken() boulevard.Token {
	return boulevard.Token{
		ID:          "TOK1",
		LibraryID:   "LIB1",
		Secret:      "K7QFM8X2N4TJ9WPR3VYB6HZQ5C",
		PeriodIndex: 2,
		ValidFrom:   boulevard.NewDate(2026, time.September, 1),
		ValidUntil:  boulevard.NewDate(2026, time.September, 30),
		State:       boulevard.TokenPending,
	}
}

func TestValidateBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		today boulevard.Date
		want  Outcome
	}{
		{"long before", boulevard.NewDate(2026, time.July, 1), OutOfWindow},
		{"day before grace opens", boulevard.NewDate(2026, time.August, 24), OutOfWindow},
		{"first day of grace", boulevard.NewDate(2026, time.August, 25), Granted},
		{"day before the window", boulevard.NewDate(2026, time.August, 31), Granted},
		{"first day of the window", boulevard.NewDate(2026, time.September, 1), Granted},
		{"mid window", boulevard.NewDate(2026, time.September, 15), Granted},
		{"last day of the window", boulevard.NewDate(2026, time.September, 30), Granted},
		{"first day after the window", boulevard.NewDate(2026, time.October, 1), Granted},
		{"last day of grace", boulevard.NewDate(2026, time.October, 7), Granted},
		{"day after grace closes", boulevard.NewDate(2026, time.October, 8), OutOfWindow},
		{"long after", boulevard.NewDate(2027, time.January, 1), OutOfWindow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Validate(septemberToken(), tc.today, GraceDays); got != tc.want {
				t.Errorf("Validate(_, %v, %d) = %v, want %v", tc.today, GraceDays, got, tc.want)
			}
		})
	}
}

func TestValidateRevokedIsInvalidEvenInsideItsWindow(t *testing.T) {
	// A revoked card must fail identically wherever it sits in time —
	// otherwise the response distinguishes a real secret from a fake one.
	tok := septemberToken()
	tok.State = boulevard.TokenRevoked
	for _, today := range []boulevard.Date{
		boulevard.NewDate(2026, time.September, 15),
		boulevard.NewDate(2026, time.August, 25),
		boulevard.NewDate(2027, time.January, 1),
	} {
		if got := Validate(tok, today, GraceDays); got != Invalid {
			t.Errorf("Validate(revoked, %v) = %v, want Invalid", today, got)
		}
	}
}

func TestValidateHonoursOtherStates(t *testing.T) {
	for _, st := range []boulevard.TokenState{
		boulevard.TokenPending, boulevard.TokenActive, boulevard.TokenExpired,
	} {
		tok := septemberToken()
		tok.State = st
		got := Validate(tok, boulevard.NewDate(2026, time.September, 15), GraceDays)
		if got != Granted {
			t.Errorf("state %q inside the window = %v, want Granted", st, got)
		}
	}
}

func TestGraceDaysIsSeven(t *testing.T) {
	if GraceDays != 7 {
		t.Errorf("GraceDays = %d, want 7 (DESIGN.md §4)", GraceDays)
	}
}

func TestOutcomeString(t *testing.T) {
	for _, tc := range []struct {
		o    Outcome
		want string
	}{
		{Invalid, "invalid"},
		{OutOfWindow, "out-of-window"},
		{Granted, "granted"},
	} {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("Outcome(%d).String() = %q, want %q", tc.o, got, tc.want)
		}
	}
}
