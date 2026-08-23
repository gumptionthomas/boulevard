package booklet

import (
	"strings"
	"testing"
)

// installSteps is the one artifact in this milestone that gets printed and
// cannot be revised afterward, so it gets the same vocabulary guard
// internal/web's TestNoTemplateAssumesALittleFreeLibrary gives the
// templates — that test walks the embedded template FS and cannot see this
// package, so the guard is duplicated here rather than left uncovered.
//
// An LFL is one demographic. A Boulevard may be a sandwich board, a garage
// door, or a fence, and copy that says "the box" or "inside the box door"
// excludes every steward who has none of those.
//
// "from the sidewalk" and "from a distance" are banned for a different
// reason: the shelf code is 1.5in today, readable only up close, so a
// distance claim describes an artifact that does not exist yet. Milestone
// 5.5 makes it true. Until then, saying it would be a lie in print.
func TestInstallStepsDoNotAssumeALittleFreeLibrary(t *testing.T) {
	banned := []string{
		"the box",
		"box door",
		"Little Free Library",
		"the hinge",
		"from the sidewalk",
		"from a distance",
	}

	for _, step := range installSteps {
		lower := strings.ToLower(step)
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("installSteps contains %q in step %q — a Boulevard may be a garage door, not a box", phrase, step)
			}
		}
	}
}
