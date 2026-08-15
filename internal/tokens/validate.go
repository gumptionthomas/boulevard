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

	// NotYet is a real, un-revoked token whose window plus grace has not
	// opened. Reachable today: a neighbour scanning a spare card from the
	// booklet, or a steward swapping a card more than seven days early —
	// which DESIGN.md §4 explicitly anticipates under force-activate.
	NotYet

	// Expired is a real, un-revoked token past its window plus grace. This
	// is deliberately NOT a failure: it is the diagnostic a steward needs in
	// order to know the card must be swapped.
	Expired

	// Granted mints a session.
	Granted
)

func (o Outcome) String() string {
	switch o {
	case NotYet:
		return "not-yet"
	case Expired:
		return "expired"
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
//
// The two sides of the window are distinct outcomes rather than one
// OutOfWindow, because they need opposite copy — "it stopped working on" is
// a lie about a card that has not started. Deciding it here keeps every
// comparison against the grace window in the one pure, boundary-tested
// place; the handler picks a page and does no date reasoning of its own.
func Validate(tok boulevard.Token, today boulevard.Date, grace int) Outcome {
	if tok.State == boulevard.TokenRevoked {
		return Invalid
	}
	if today.Before(tok.ValidFrom.AddDays(-grace)) {
		return NotYet
	}
	if today.After(tok.ValidUntil.AddDays(grace)) {
		return Expired
	}
	return Granted
}
