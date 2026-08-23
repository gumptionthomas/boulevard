package web

import (
	"io/fs"
	"strings"
	"testing"
)

// No user-facing template may assume a Little Free Library.
//
// An LFL is one demographic. A Boulevard may be a sandwich board, a garage
// door, or a fence, and copy that says "the box" or "inside the box door"
// excludes every steward who has none of those. This guards the vocabulary
// against a future page quietly reintroducing it — the sweep that removed
// it touched nineteen templates, and the twentieth is the one nobody
// remembers.
//
// "in the door" is the same assumption wearing the active card's name. The
// first sweep banned the noun and left the metaphor standing in thirteen
// places, including a button that told a steward with a fence to put a card
// somewhere they have not got. User-facing copy says "at the shelf" for the
// physical spot, or "in place" where "at the shelf" would be the third
// "shelf" in one sentence.
//
// "from the sidewalk" and "from a distance" are banned for a different
// reason: the two printed codes are 1.5in and 1.15in, both readable only up
// close, so a distance claim describes an artifact that does not exist yet.
// Milestone 5.5 makes it true. Until then, saying it would be a lie in
// print.
func TestNoTemplateAssumesALittleFreeLibrary(t *testing.T) {
	banned := []string{
		"the box",
		"box door",
		"in the door",
		"Little Free Library",
		"the hinge",
		"from the sidewalk",
		"from a distance",
	}

	visited := 0
	err := fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		visited++
		b, readErr := templateFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(b))
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("%s contains %q — a Boulevard may be a garage door, not a box", path, phrase)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	// Floor against silent narrowing: a glob that stopped matching, a moved
	// directory, or an empty FS. Deliberately well below the real count (20).
	if visited < 15 {
		t.Fatalf("walk visited %d templates, expected at least 15", visited)
	}
}
