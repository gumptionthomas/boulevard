package booklet

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixedRenderer() Renderer {
	return Renderer{CreationDate: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)}
}

func TestRenderProducesAPDF(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Errorf("output does not start with a PDF header: %q", out[:min(8, len(out))])
	}
	if len(out) < 1000 {
		t.Errorf("output is %d bytes, suspiciously small for a two-page booklet", len(out))
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	// Golden-file testing is only possible if identical inputs produce
	// identical bytes. Creation date must be injected, never ambient.
	in := testInput(t)
	p, _ := BuildPlan(in)
	a, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two renders of the same plan differ; something ambient (time, map order, randomness) leaked in")
	}
}

func TestRenderGolden(t *testing.T) {
	p, err := BuildPlan(fixedInput(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "booklet.pdf")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/booklet -update` to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("rendered output differs from %s (got %d bytes, want %d). "+
			"If the change is intended, re-run with -update and inspect the PDF by eye.",
			golden, len(got), len(want))
	}
}

// The translator is built from a throwaway document, so it works with no
// render in flight and cannot depend on which *Fpdf happened to ask first.
func TestToCP1252TranslatesBeyondASCII(t *testing.T) {
	const dash = "Aug 14 – Aug 31" // U+2013, which core fonts cannot address directly
	got := toCP1252(dash)
	if got == dash {
		t.Errorf("toCP1252(%q) returned it unchanged; the en dash would print as mojibake", dash)
	}
	if len(got) != len([]rune(dash)) {
		t.Errorf("toCP1252(%q) = %q; cp1252 is one byte per character", dash, got)
	}
	if plain := "CARD 1 OF 12"; toCP1252(plain) != plain {
		t.Errorf("toCP1252(%q) = %q; ASCII must pass through unchanged", plain, toCP1252(plain))
	}
}

// Spec §8.1: tick marks extend into the margins so a straightedge can be
// aligned. Every cut the steward makes needs them at both ends — including
// the bottom edge of the last row and the vertical cut between the columns,
// which had no margin guide at all.
func TestEveryCutLineIsTickedAtBothEnds(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}

	has := func(ticks []Tick, want Tick) bool {
		for _, got := range ticks {
			if got == want {
				return true
			}
		}
		return false
	}

	// Sheet 1: a full 2x5 grid. Six horizontal cuts (the grid's top and
	// bottom edges included) and three vertical ones.
	sheet1 := cutTicks(p.Sheets[0])
	for row := 0; row <= Rows; row++ {
		y := MarginY + float64(row)*CardH
		if !has(sheet1, Tick{MarginX - TickLen, y, MarginX, y}) {
			t.Errorf("no left-margin tick for the horizontal cut at y=%.0f", y)
		}
		if !has(sheet1, Tick{PageW - MarginX, y, PageW - MarginX + TickLen, y}) {
			t.Errorf("no right-margin tick for the horizontal cut at y=%.0f", y)
		}
	}
	for col := 0; col <= Cols; col++ {
		x := MarginX + float64(col)*CardW
		if !has(sheet1, Tick{x, MarginY - TickLen, x, MarginY}) {
			t.Errorf("no top-margin tick for the vertical cut at x=%.0f", x)
		}
		if !has(sheet1, Tick{x, PageH - MarginY, x, PageH - MarginY + TickLen}) {
			t.Errorf("no bottom-margin tick for the vertical cut at x=%.0f", x)
		}
	}

	// Sheet 2: the cover fills rows 1-4, so the vertical cut runs only
	// through the bottom strip. A tick above the cover would invite a cut
	// straight down through it.
	sheet2 := cutTicks(p.Sheets[1])
	cut := MarginY + CoverH
	if !has(sheet2, Tick{MarginX - TickLen, cut, MarginX, cut}) {
		t.Errorf("no left-margin tick for the cut that frees the cover at y=%.0f", cut)
	}
	if !has(sheet2, Tick{PageW - MarginX, cut, PageW - MarginX + TickLen, cut}) {
		t.Errorf("no right-margin tick for the cut that frees the cover at y=%.0f", cut)
	}
	mid := MarginX + CardW
	if !has(sheet2, Tick{mid, PageH - MarginY, mid, PageH - MarginY + TickLen}) {
		t.Errorf("no bottom-margin tick for the vertical cut at x=%.0f", mid)
	}
	for _, tick := range sheet2 {
		if tick.Y1 < MarginY && tick.X1 == tick.X2 && tick.X1 > MarginX && tick.X1 < PageW-MarginX {
			t.Errorf("tick %+v sits above the cover, inviting a vertical cut through it", tick)
		}
	}
}

// Ticks belong in the margins, where no blade passes. One drawn across a
// card would print a line through the artwork.
func TestTicksStayInTheMargins(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	for si, sheet := range p.Sheets {
		for _, tick := range cutTicks(sheet) {
			inMarginX := (tick.X1 < MarginX && tick.X2 <= MarginX) || (tick.X1 >= PageW-MarginX && tick.X2 > PageW-MarginX)
			inMarginY := (tick.Y1 < MarginY && tick.Y2 <= MarginY) || (tick.Y1 >= PageH-MarginY && tick.Y2 > PageH-MarginY)
			if !inMarginX && !inMarginY {
				t.Errorf("sheet %d tick %+v is not confined to a margin", si, tick)
			}
			if tick.X1 < 0 || tick.Y1 < 0 || tick.X2 > PageW || tick.Y2 > PageH {
				t.Errorf("sheet %d tick %+v runs off the page", si, tick)
			}
		}
	}
}

func TestRenderRejectsAnEmptyPlan(t *testing.T) {
	if _, err := fixedRenderer().Render(Plan{}); err == nil {
		t.Error("Render(Plan{}) succeeded, want an error")
	}
}
