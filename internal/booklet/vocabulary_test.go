package booklet

import (
	"strconv"
	"strings"
	"testing"
)

// The booklet is the one artifact in this milestone that gets printed and
// cannot be revised afterward, so its copy gets the same vocabulary guard
// internal/web's TestNoTemplateAssumesALittleFreeLibrary gives the
// templates — that test walks the embedded template FS and cannot see this
// package, so the guard is duplicated here rather than left uncovered. The
// two ban lists are identical on purpose: a phrase banned on screen and
// allowed on paper is the phrase that ends up on paper.
//
// It walks every string that reaches the page, not just installSteps.
// DESIGN.md §4 says this counterpart guards "the booklet cover", and for
// one milestone it did not: SleeveWarning, SignageLine, SignVerb,
// SignSubtitle and CardVerb all print and none was covered — and
// SleeveWarning was the last string this branch changed. Anything added to
// geometry.go's "copy that must not drift" block belongs in printedCopy
// below; the emptiness check at the end is what catches a constant that
// was gutted rather than removed.
//
// An LFL is one demographic. A Boulevard may be a sandwich board, a garage
// door, or a fence, and copy that says "the box" or "inside the box door"
// excludes every steward who has none of those. "in the door" is the same
// assumption wearing the active card's name.
//
// "from the sidewalk" and "from a distance" are banned for a different
// reason: the shelf code is 1.5in today, readable only up close, so a
// distance claim describes an artifact that does not exist yet. Milestone
// 5.5 makes it true. Until then, saying it would be a lie in print.
func TestPrintedCopyDoesNotAssumeALittleFreeLibrary(t *testing.T) {
	banned := []string{
		"the box",
		"box door",
		"in the door",
		"Little Free Library",
		"the hinge",
		"from the sidewalk",
		"from a distance",
	}

	type printed struct{ name, text string }
	printedCopy := []printed{
		{"CardVerb", CardVerb},
		{"SignVerb", SignVerb},
		{"SignSubtitle", SignSubtitle},
		{"SignageLine", SignageLine},
		{"SleeveWarning", SleeveWarning},
	}
	for i, step := range installSteps {
		printedCopy = append(printedCopy, printed{"installSteps[" + strconv.Itoa(i) + "]", step})
	}

	for _, p := range printedCopy {
		if strings.TrimSpace(p.text) == "" {
			t.Errorf("%s is empty; the guard is reading nothing where it thinks it reads print", p.name)
			continue
		}
		lower := strings.ToLower(p.text)
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("%s contains %q in %q — a Boulevard may be a garage door, not a box", p.name, phrase, p.text)
			}
		}
	}
}
