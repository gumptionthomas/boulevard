package booklet

import "testing"

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
