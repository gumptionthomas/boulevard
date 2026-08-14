package booklet

import (
	"fmt"
	"sort"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// PlacedCard is one monthly card and where it sits on its sheet.
type PlacedCard struct {
	Rect    Rect
	Month   string
	Year    int
	From    boulevard.Date
	Until   boulevard.Date
	Index   int // 1-based
	Of      int
	Payload string
}

type Cover struct {
	Rect          Rect
	LibraryName   string
	LocationLabel string
	SignPayload   string
	SourceURL     string
	BuildLine     string
}

type Sheet struct {
	Cards []PlacedCard
	Cover *Cover
}

type Plan struct{ Sheets []Sheet }

type Input struct {
	Library   boulevard.Library
	Tokens    []boulevard.Token
	SourceURL string
	BuildLine string
}

// CardsPerSheet is a full grid.
const CardsPerSheet = Cols * Rows // 10

// BuildPlan computes pure layout data. It never touches a PDF library, so
// every placement rule here is unit-testable on its own.
func BuildPlan(in Input) (Plan, error) {
	if len(in.Tokens) != tokens.PeriodCount {
		return Plan{}, fmt.Errorf("booklet needs exactly %d tokens, got %d", tokens.PeriodCount, len(in.Tokens))
	}
	if in.Library.BaseURL == "" {
		return Plan{}, fmt.Errorf("library %q has no base URL", in.Library.Slug)
	}

	toks := append([]boulevard.Token(nil), in.Tokens...)
	sort.Slice(toks, func(i, j int) bool { return toks[i].PeriodIndex < toks[j].PeriodIndex })

	sheet1 := Sheet{Cards: make([]PlacedCard, 0, CardsPerSheet)}
	for i := 0; i < CardsPerSheet; i++ {
		sheet1.Cards = append(sheet1.Cards, placeCard(toks[i], CardRect(i), in.Library.BaseURL))
	}

	// Sheet 2: the cover fills rows 1..4, the remaining cards sit in row 5.
	// That gives one horizontal cut and one vertical cut, and the cover
	// survives intact.
	sheet2 := Sheet{
		Cover: &Cover{
			Rect:          Rect{X: MarginX, Y: MarginY, W: GridW, H: CoverH},
			LibraryName:   in.Library.Name,
			LocationLabel: in.Library.LocationLabel,
			SignPayload:   in.Library.BaseURL,
			SourceURL:     in.SourceURL,
			BuildLine:     in.BuildLine,
		},
	}
	for i := CardsPerSheet; i < len(toks); i++ {
		slot := CardsPerSheet - Cols + (i - CardsPerSheet) // final row of the grid
		sheet2.Cards = append(sheet2.Cards, placeCard(toks[i], CardRect(slot), in.Library.BaseURL))
	}

	return Plan{Sheets: []Sheet{sheet1, sheet2}}, nil
}

func placeCard(tok boulevard.Token, r Rect, baseURL string) PlacedCard {
	return PlacedCard{
		Rect:    r,
		Month:   tok.ValidFrom.MonthName(),
		Year:    tok.ValidFrom.Year,
		From:    tok.ValidFrom,
		Until:   tok.ValidUntil,
		Index:   tok.PeriodIndex,
		Of:      tokens.PeriodCount,
		Payload: baseURL + "/s/" + tok.Secret,
	}
}
