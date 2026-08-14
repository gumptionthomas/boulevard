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

func TestRenderRejectsAnEmptyPlan(t *testing.T) {
	if _, err := fixedRenderer().Render(Plan{}); err == nil {
		t.Error("Render(Plan{}) succeeded, want an error")
	}
}
