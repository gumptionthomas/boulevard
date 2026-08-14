package booklet

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// inputFrom builds the booklet input every test in this package uses,
// drawing its secrets from entropy.
//
// There is exactly one of these on purpose. Two near-identical builders —
// one for the golden file, one for everything else — meant the golden could
// be edited to test a different base URL or install date from the rest of
// the suite, silently, and go on passing.
func inputFrom(t *testing.T, entropy io.Reader) Input {
	t.Helper()
	id, err := boulevard.NewLibraryID(entropy)
	if err != nil {
		t.Fatal(err)
	}
	lib := boulevard.Library{
		ID: id, Slug: "fairview", Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       "https://boulevard.example.org",
	}
	periods := tokens.Periods(boulevard.NewDate(2026, time.August, 14), tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(entropy)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, boulevard.Token{
			LibraryID: id, Secret: secret, PeriodIndex: p.Index,
			ValidFrom: p.From, ValidUntil: p.Until, State: boulevard.TokenPending,
		})
	}
	return Input{
		Library: lib, Tokens: toks,
		SourceURL: "https://github.com/gumptionthomas/boulevard",
		BuildLine: "boulevard 0.1.0 (abc1234)",
	}
}

func testInput(t *testing.T) Input {
	t.Helper()
	return inputFrom(t, rand.Reader)
}

// fixedInput is testInput with a fixed byte source, so the golden PDF is
// stable across runs.
func fixedInput(t *testing.T) Input {
	t.Helper()
	return inputFrom(t, bytes.NewReader(bytes.Repeat([]byte("boulevard-golden!"), 64)))
}

func TestPlanHasTwoSheets(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Sheets) != 2 {
		t.Fatalf("got %d sheets, want 2", len(p.Sheets))
	}
	if len(p.Sheets[0].Cards) != 10 {
		t.Errorf("sheet 1 has %d cards, want 10", len(p.Sheets[0].Cards))
	}
	if len(p.Sheets[1].Cards) != 2 {
		t.Errorf("sheet 2 has %d cards, want 2", len(p.Sheets[1].Cards))
	}
}

func TestCoverIsOnExactlyOneSheet(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	covers := 0
	for _, s := range p.Sheets {
		if s.Cover != nil {
			covers++
		}
	}
	if covers != 1 {
		t.Errorf("found %d covers, want exactly 1", covers)
	}
	if p.Sheets[0].Cover != nil {
		t.Error("cover must not be on sheet 1 — that sheet is a clean 2x5 grid of cards")
	}
}

func TestEverySecretAppearsExactlyOnce(t *testing.T) {
	in := testInput(t)
	p, _ := BuildPlan(in)
	count := map[string]int{}
	for _, s := range p.Sheets {
		for _, c := range s.Cards {
			count[c.Payload]++
		}
	}
	if len(count) != tokens.PeriodCount {
		t.Errorf("got %d distinct payloads, want %d", len(count), tokens.PeriodCount)
	}
	for _, tok := range in.Tokens {
		want := in.Library.BaseURL + "/s/" + tok.Secret
		if count[want] != 1 {
			t.Errorf("payload for period %d appears %d times, want 1", tok.PeriodIndex, count[want])
		}
	}
}

func TestCardsCarryOrderingMetadata(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	var seen []int
	for _, s := range p.Sheets {
		for _, c := range s.Cards {
			seen = append(seen, c.Index)
			if c.Of != tokens.PeriodCount {
				t.Errorf("card %d says 'of %d', want %d", c.Index, c.Of, tokens.PeriodCount)
			}
		}
	}
	for i, idx := range seen {
		if idx != i+1 {
			t.Errorf("card at position %d has Index %d; cards must be laid out in period order", i, idx)
		}
	}
}

func TestFirstCardCarriesItsMonthAndDates(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	c := p.Sheets[0].Cards[0]
	if c.Month != "August" || c.Year != 2026 {
		t.Errorf("first card = %s %d, want August 2026", c.Month, c.Year)
	}
	if !c.From.Equal(boulevard.NewDate(2026, time.August, 14)) {
		t.Errorf("From = %v, want 2026-08-14", c.From)
	}
	if !c.Until.Equal(boulevard.NewDate(2026, time.August, 31)) {
		t.Errorf("Until = %v, want 2026-08-31", c.Until)
	}
}

func TestEveryRectFitsInsideThePrintableArea(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	for si, s := range p.Sheets {
		for _, c := range s.Cards {
			assertInsidePage(t, si, "card", c.Rect)
		}
		if s.Cover != nil {
			assertInsidePage(t, si, "cover", s.Cover.Rect)
		}
	}
}

func assertInsidePage(t *testing.T, sheet int, what string, r Rect) {
	t.Helper()
	if r.X < MarginX || r.Y < MarginY {
		t.Errorf("sheet %d %s at (%.1f,%.1f) starts inside the margin", sheet, what, r.X, r.Y)
	}
	if r.X+r.W > PageW-MarginX+0.01 {
		t.Errorf("sheet %d %s right edge %.1f exceeds %.1f", sheet, what, r.X+r.W, PageW-MarginX)
	}
	if r.Y+r.H > PageH-MarginY+0.01 {
		t.Errorf("sheet %d %s bottom edge %.1f exceeds %.1f", sheet, what, r.Y+r.H, PageH-MarginY)
	}
}

func TestCoverSitsAboveTheCardRowOnSheetTwo(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	s := p.Sheets[1]
	cut := s.Cover.Rect.Y + s.Cover.Rect.H
	for _, c := range s.Cards {
		if c.Rect.Y < cut-0.01 {
			t.Errorf("card at y=%.1f overlaps the cover, which ends at y=%.1f", c.Rect.Y, cut)
		}
	}
	if cut != MarginY+CoverH {
		t.Errorf("cut line at %.1f, want %.1f", cut, MarginY+CoverH)
	}
}

func TestBuildPlanRejectsWrongTokenCount(t *testing.T) {
	in := testInput(t)
	in.Tokens = in.Tokens[:5]
	if _, err := BuildPlan(in); err == nil {
		t.Error("BuildPlan accepted 5 tokens, want an error")
	}
}

func TestSignPayloadIsTheBareBaseURL(t *testing.T) {
	in := testInput(t)
	p, _ := BuildPlan(in)
	if got := p.Sheets[1].Cover.SignPayload; got != in.Library.BaseURL {
		t.Errorf("SignPayload = %q, want the bare base URL %q", got, in.Library.BaseURL)
	}
}
