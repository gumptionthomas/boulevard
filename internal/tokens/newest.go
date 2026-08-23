package tokens

import "github.com/gumptionthomas/boulevard/internal/boulevard"

// NewestBooklet selects the tokens belonging to a library's highest-numbered
// booklet: those with period_index at or above the last multiple-of-twelve
// boundary at or below the highest period_index present. On a library that
// has never rotated, that is every token it holds.
//
// This exists because booklet.BuildPlan refuses anything that is not
// exactly twelve tokens, and rotation makes period_index monotonic rather
// than restarting it at 1 (DESIGN.md §6): a library holds thirteen or more
// tokens the moment it has rotated once, twenty-five or more after twice,
// and so on. Every path that renders a PDF has to select one booklet's
// worth before calling BuildPlan — there are three such paths
// (`boulevard booklet`'s plain reprint, `boulevard booklet --rotate`, and
// the steward desk's booklet download), and before this function existed
// two of them carried their own copy of this arithmetic and the third had
// none. The one with none is how a plain reprint shipped broken after any
// rotation: BuildPlan's exactly-twelve refusal surfaced as an opaque
// database-looking error instead of never occurring.
//
// This function only selects. It does not decide what a short result
// means — a caller holding fewer than PeriodCount tokens back (a rotation
// caught mid-transaction, or a library with no tokens at all) reports that
// however fits its own surface: an HTTP 409, a CLI refusal. Callers that
// need exactly twelve check len() themselves.
func NewestBooklet(toks []boulevard.Token) []boulevard.Token {
	maxIndex := 0
	for _, tok := range toks {
		if tok.PeriodIndex > maxIndex {
			maxIndex = tok.PeriodIndex
		}
	}
	if maxIndex == 0 {
		return nil
	}
	start := ((maxIndex-1)/PeriodCount)*PeriodCount + 1

	out := make([]boulevard.Token, 0, PeriodCount)
	for _, tok := range toks {
		if tok.PeriodIndex >= start {
			out = append(out, tok)
		}
	}
	return out
}
