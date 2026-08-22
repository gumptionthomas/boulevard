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
