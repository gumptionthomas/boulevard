package boulevard

import (
	"strings"
	"unicode"
)

// Sanitize replaces every control character with a space.
//
// Two surfaces here render stranger-supplied text into something that reads
// control characters as instructions rather than as content: the steward's
// terminal (`boulevard queue` prints a note, a payload and an attribution)
// and the steward's log file (the request log prints a percent-decoded
// path). A note of a thousand runes may legally contain "\x1b[1A\x1b[2K",
// which erases the line above it, and embedded newlines, which start a new
// one — enough to draw a forged queue entry over a real one, so the steward
// reads one item and approves a different one. That is the same failure
// resolvePrefix guards against, arriving through the display instead of the
// resolver.
//
// It belongs at the print boundary, never in validation. What was stored
// should stay faithful to what the person wrote; only the rendering of it
// into a control-sensitive stream needs defusing.
//
// A space rather than a deletion, so "a\x1b[2Kb" cannot close up into a word
// that was never typed, and so the reader can see that something was there.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
