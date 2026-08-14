package tokens

import "github.com/gumptionthomas/boulevard/internal/boulevard"

// PeriodCount is the number of cards in a booklet (DESIGN.md §4).
const PeriodCount = 12

type Period struct {
	Index int
	From  boulevard.Date
	Until boulevard.Date
}

// Periods assigns explicit calendar periods starting at install.
//
// Period 1 runs from the install date to the end of that calendar month,
// however short. Periods 2..n are whole calendar months. The short first
// period is deliberate: it aligns every later card to a month boundary.
//
// Periods are stored, never derived from a clock at validation time —
// DESIGN.md §4 rejects TOTP-style derivation because drift, DST, and a late
// card swap all become silent auth failures with no recovery path.
func Periods(install boulevard.Date, n int) []Period {
	if n <= 0 {
		return nil
	}
	out := make([]Period, 0, n)
	out = append(out, Period{Index: 1, From: install, Until: install.LastOfMonth()})

	cur := install.NextMonth() // always the first of the following month
	for i := 2; i <= n; i++ {
		out = append(out, Period{Index: i, From: cur, Until: cur.LastOfMonth()})
		cur = cur.NextMonth()
	}
	return out
}
