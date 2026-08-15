package boulevard

import (
	"strings"
	"testing"
	"unicode"
)

func TestSanitizeReplacesControlCharacters(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text is untouched", "Reminded me of the alley cat.", "Reminded me of the alley cat."},
		{"newline", "one\ntwo", "one two"},
		{"carriage return", "one\r\ntwo", "one  two"},
		{"tab", "one\ttwo", "one two"},
		{"an ANSI cursor escape", "safe\x1b[1A\x1b[2Kforged", "safe [1A [2Kforged"},
		{"a bare escape", "\x1b", " "},
		{"a NUL", "a\x00b", "a b"},
		{"DEL", "a\x7fb", "a b"},
		{"non-ASCII text survives", "café · über", "café · über"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A space rather than a deletion: "a\x1b[2Kb" must not close up into "ab",
// which would let a submission read as a word nobody typed.
func TestSanitizeDoesNotCloseUpTheGap(t *testing.T) {
	if got := Sanitize("a\x1bb"); got != "a b" {
		t.Errorf("Sanitize = %q, want %q", got, "a b")
	}
}

// The whole point is that nothing control-shaped survives, whatever it was.
func TestSanitizeLeavesNoControlRunes(t *testing.T) {
	var b strings.Builder
	for r := rune(0); r < 0x100; r++ {
		b.WriteRune(r)
	}
	for _, r := range Sanitize(b.String()) {
		if unicode.IsControl(r) {
			t.Fatalf("control rune %U survived Sanitize", r)
		}
	}
}

// Sanitize is a display helper, not a validator: what a person wrote is
// stored as they wrote it, so a later milestone can still render it into
// something that is not a terminal.
func TestValidateSubmissionStoresControlCharactersFaithfully(t *testing.T) {
	in := linkSubmission()
	in.Note = "line one\nline two"
	got, errs := ValidateSubmission(in)
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if got.Note != "line one\nline two" {
		t.Errorf("Note = %q; validation must not rewrite what was typed", got.Note)
	}
}
