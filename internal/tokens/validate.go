package tokens

import "github.com/gumptionthomas/boulevard/internal/boulevard"

// GraceDays extends a token's window at both ends (DESIGN.md §4).
//
// The grace window is what makes a human-swapped paper card work: a steward
// who puts the new card up four days late has not locked anyone out.
const GraceDays = 7

// Outcome is what a scan resolves to.
type Outcome int

const (
	// Invalid covers an unknown secret and a revoked one alike. They must be
	// indistinguishable, or the response confirms to whoever photographed a
	// card that its secret was real (DESIGN.md §4).
	Invalid Outcome = iota

	// OutOfWindow is a real, un-revoked token outside its window plus grace.
	// This is deliberately NOT a failure: it is the diagnostic a steward
	// needs in order to know the card must be swapped.
	OutOfWindow

	// Granted mints a session.
	Granted
)

func (o Outcome) String() string {
	switch o {
	case OutOfWindow:
		return "out-of-window"
	case Granted:
		return "granted"
	default:
		return "invalid"
	}
}

// Validate decides a scan's outcome. It is pure: no clock, no I/O, no store.
//
// The caller resolves the secret first; a miss is Invalid without ever
// reaching here. `today` is a parameter rather than a clock read so the
// grace boundaries can be tested on the exact day.
func Validate(tok boulevard.Token, today boulevard.Date, grace int) Outcome {
	if tok.State == boulevard.TokenRevoked {
		return Invalid
	}
	from := tok.ValidFrom.AddDays(-grace)
	until := tok.ValidUntil.AddDays(grace)
	if today.Before(from) || today.After(until) {
		return OutOfWindow
	}
	return Granted
}
