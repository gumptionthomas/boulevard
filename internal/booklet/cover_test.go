package booklet

import (
	"strings"
	"testing"
)

func TestSignRectIsCenteredInsideTheCover(t *testing.T) {
	cover := Rect{X: MarginX, Y: MarginY, W: GridW, H: CoverH}
	got := SignRect(cover)

	if got.W != SignW || got.H != SignH {
		t.Errorf("sign is %.1fx%.1f, want %.1fx%.1f", got.W, got.H, SignW, SignH)
	}
	wantX := cover.X + (cover.W-SignW)/2
	if got.X != wantX {
		t.Errorf("sign X = %.2f, want %.2f (horizontally centred)", got.X, wantX)
	}
	if got.X < cover.X || got.X+got.W > cover.X+cover.W {
		t.Error("sign escapes the cover horizontally")
	}
	if got.Y < cover.Y || got.Y+got.H > cover.Y+cover.H {
		t.Error("sign escapes the cover vertically")
	}
}

func TestCoverRendersWithoutError(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A rendered cover adds a second QR and a block of copy; a booklet with
	// a stubbed-out cover is markedly smaller.
	if len(out) < 5000 {
		t.Errorf("output is %d bytes, too small to contain a rendered cover", len(out))
	}
}

func TestTooLongLibraryNameProducesAnError(t *testing.T) {
	in := testInput(t)
	// Wraps to 3 lines on the browse sign at its 14pt Times Bold, well past
	// the 2-line budget the sign's fixed height affords.
	in.Library.Name = "North Fourteenth Street Little Free Library and Boulevard"
	p, err := BuildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixedRenderer().Render(p)
	if err == nil {
		t.Fatal("Render succeeded with a library name too long for the browse sign, want an error")
	}
	if n := strings.Count(err.Error(), "browse sign:"); n != 1 {
		t.Errorf("error says %q; %q appears %d times, want 1", err, "browse sign:", n)
	}
}

// ValidateSignName exists so the CLI can refuse a too-long name as a usage
// error before anything is written. It is only worth having if it agrees
// exactly with the renderer it is standing in for.
func TestValidateSignNameAgreesWithTheRenderer(t *testing.T) {
	names := []string{
		"The Fairview Boulevard",
		"Powderhorn Park Little Free Library", // an ordinary name; must fit
		"The Whittier Community Boulevard",    // exactly two lines
		"Highland Park Neighborhood Little Free Library",
		"North Fourteenth Street Little Free Library and Boulevard",
		"Ümlaut Box", // non-ASCII must be measured, not crash
	}
	for _, name := range names {
		in := testInput(t)
		in.Library.Name = name
		p, err := BuildPlan(in)
		if err != nil {
			t.Fatal(err)
		}
		_, renderErr := fixedRenderer().Render(p)
		validateErr := ValidateSignName(name)
		if (validateErr == nil) != (renderErr == nil) {
			t.Errorf("name %q: ValidateSignName = %v but Render = %v; the pre-flight must agree with the renderer",
				name, validateErr, renderErr)
		}
	}
}

func TestValidateSignNameAcceptsAnOrdinaryLittleFreeLibraryName(t *testing.T) {
	if err := ValidateSignName("Powderhorn Park Little Free Library"); err != nil {
		t.Errorf("ValidateSignName: %v, want an ordinary Little Free Library name to fit", err)
	}
}

func TestValidateSignNameSaysWhatToDoAboutIt(t *testing.T) {
	err := ValidateSignName("Highland Park Neighborhood Little Free Library")
	if err == nil {
		t.Fatal("ValidateSignName accepted a name that does not fit the sign")
	}
	msg := err.Error()
	for _, want := range []string{"browse sign", "characters", "--name"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q; a steward needs a remedy, not geometry", msg, want)
		}
	}
	if strings.Contains(msg, "pt of width") {
		t.Errorf("message %q reports points; a steward cannot act on typographic units", msg)
	}
}

func TestTwoLineLibraryNameRendersWithoutError(t *testing.T) {
	in := testInput(t)
	// Wraps to exactly 2 lines on the browse sign — within the budget.
	in.Library.Name = "The Whittier Community Boulevard"
	p, err := BuildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixedRenderer().Render(p); err != nil {
		t.Errorf("Render: %v, want a two-line library name to fit the browse sign", err)
	}
}
