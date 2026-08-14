package booklet

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"
)

// Ink and paper. Kept as a palette so the card and the sign stay consistent.
const (
	inkR, inkG, inkB       = 20, 19, 13    // #14130D
	paperR, paperG, paperB = 253, 252, 249 // #FDFCF9
	ruleR, ruleG, ruleB    = 201, 195, 178
)

// Renderer draws a Plan to PDF bytes.
//
// CreationDate is a field rather than time.Now() so that identical inputs
// render to identical bytes, which is what makes golden testing possible.
type Renderer struct {
	CreationDate time.Time
}

func (r Renderer) Render(p Plan) ([]byte, error) {
	if len(p.Sheets) == 0 {
		return nil, errors.New("plan has no sheets")
	}

	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetCreationDate(r.CreationDate)
	// fpdf defaults ModDate to time.Now() unless told otherwise; left alone
	// it would make every render's bytes differ, defeating golden testing.
	pdf.SetModificationDate(r.CreationDate)
	// fpdf's internal font/template catalogs are Go maps, walked in
	// (deliberately) random order unless this is set. Without it, two
	// renders of the same Plan assign PDF object numbers differently and
	// produce different bytes even though nothing meaningful changed.
	pdf.SetCatalogSort(true)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)

	for _, sheet := range p.Sheets {
		pdf.AddPage()
		if sheet.Cover != nil {
			if err := drawCover(pdf, *sheet.Cover); err != nil {
				return nil, fmt.Errorf("draw cover: %w", err)
			}
		}
		for _, c := range sheet.Cards {
			if err := drawCard(pdf, c); err != nil {
				return nil, fmt.Errorf("draw card %d: %w", c.Index, err)
			}
		}
		drawCutMarks(pdf, sheet)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// drawQR paints a symbol as vector rectangles, merging horizontal runs of
// dark modules into single rects. Vector keeps the code crisp at any print
// size; run-merging keeps the file small — a naive per-module version emits
// well over ten thousand rectangles per booklet.
func drawQR(pdf *fpdf.Fpdf, c Code, x, y, size float64) {
	m := size / float64(c.Size)
	pdf.SetFillColor(inkR, inkG, inkB)
	for row := 0; row < c.Size; row++ {
		col := 0
		for col < c.Size {
			if !c.Modules[row][col] {
				col++
				continue
			}
			run := 1
			for col+run < c.Size && c.Modules[row][col+run] {
				run++
			}
			pdf.Rect(x+float64(col)*m, y+float64(row)*m, float64(run)*m, m, "F")
			col += run
		}
	}
}

// cp1252 is the encoding fpdf's core fonts expect: they carry no encoding
// of their own, so any character past plain ASCII — an en dash in a date
// range, an accented library name — must be mapped to its cp1252 byte
// before it reaches Text(), or it prints as mojibake instead of the glyph.
//
// The mapping table ships inside fpdf as a static embedded resource and
// depends on nothing about the document being drawn, so it is built once
// from a throwaway document of its own. Building it from whichever *Fpdf
// happened to call first made the parameter a lie on every later call, and
// left a nil function — and a panic on every use — if it ever failed.
var cp1252 = sync.OnceValue(func() func(string) string {
	fn := fpdf.New("P", "pt", "Letter", "").UnicodeTranslatorFromDescriptor("")
	if fn == nil {
		// fpdf reports failure by leaving the translator nil. Pass text
		// through rather than panic: mojibake in one accented name is a far
		// better outcome than no booklet at all.
		return func(s string) string { return s }
	}
	return fn
})

// toCP1252 translates s for display with a core font. Safe to call with
// plain ASCII text too — it passes through unchanged.
func toCP1252(s string) string { return cp1252()(s) }

// drawTracked renders letterspaced text. fpdf has no native tracking, so
// characters are placed individually — which also means each character can
// be encoded independently, so tracked text need not be plain ASCII.
func drawTracked(pdf *fpdf.Fpdf, x, y float64, text string, tracking float64) {
	cur := x
	for _, ch := range text {
		s := toCP1252(string(ch))
		pdf.Text(cur, y, s)
		cur += pdf.GetStringWidth(s) + tracking
	}
}

// trackedWidth is the width drawTracked will occupy, for centering.
func trackedWidth(pdf *fpdf.Fpdf, text string, tracking float64) float64 {
	var w float64
	for _, ch := range text {
		w += pdf.GetStringWidth(toCP1252(string(ch))) + tracking
	}
	if w > 0 {
		w -= tracking // no trailing gap
	}
	return w
}

// TickLen is how far a cut mark reaches into the margin. Long enough to
// lay a straightedge against, short enough not to reach the paper's edge.
const TickLen = 12.0

// drawCutMarks draws hairlines on the shared card edges, plus a tick at
// each end of every cut the steward has to make — out in the margin, where
// no blade passes. Scissors, not a guillotine (DESIGN.md §4).
//
// Every cut gets marks in the direction it runs: horizontal cuts are ticked
// in the left and right margins, the vertical cut between the two card
// columns in the top and bottom margins. A tick is only drawn where cards
// actually reach the margin, which is what keeps sheet 2 from appearing to
// invite a vertical cut straight down through the cover.
func drawCutMarks(pdf *fpdf.Fpdf, sheet Sheet) {
	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)

	for _, c := range sheet.Cards {
		pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "D")
	}
	for _, t := range cutTicks(sheet) {
		pdf.Line(t.X1, t.Y1, t.X2, t.Y2)
	}
}

// Tick is one cut mark: a short segment lying in a page margin.
type Tick struct{ X1, Y1, X2, Y2 float64 }

// cutTicks is where the tick geometry is decided, kept free of the PDF
// library so the marks can be asserted directly rather than inferred from
// rendered bytes.
func cutTicks(sheet Sheet) []Tick {
	// Each cut line, mapped to the extent of what it separates.
	horizontal := map[float64]span{} // y of the cut -> the x range it spans
	vertical := map[float64]span{}   // x of the cut -> the y range it spans
	for _, c := range sheet.Cards {
		l, r := c.Rect.X, c.Rect.X+c.Rect.W
		t, b := c.Rect.Y, c.Rect.Y+c.Rect.H
		addSpan(horizontal, t, l, r)
		addSpan(horizontal, b, l, r)
		addSpan(vertical, l, t, b)
		addSpan(vertical, r, t, b)
	}
	if sheet.Cover != nil {
		// The single horizontal cut that frees the cover.
		addSpan(horizontal, sheet.Cover.Rect.Y+sheet.Cover.Rect.H,
			sheet.Cover.Rect.X, sheet.Cover.Rect.X+sheet.Cover.Rect.W)
	}

	// Sorted, because map order is random and the golden file compares
	// bytes: the same plan must draw the same lines in the same sequence.
	var ticks []Tick
	for _, y := range slices.Sorted(maps.Keys(horizontal)) {
		s := horizontal[y]
		if s.lo <= MarginX {
			ticks = append(ticks, Tick{MarginX - TickLen, y, MarginX, y})
		}
		if s.hi >= PageW-MarginX {
			ticks = append(ticks, Tick{PageW - MarginX, y, PageW - MarginX + TickLen, y})
		}
	}
	for _, x := range slices.Sorted(maps.Keys(vertical)) {
		s := vertical[x]
		if s.lo <= MarginY {
			ticks = append(ticks, Tick{x, MarginY - TickLen, x, MarginY})
		}
		if s.hi >= PageH-MarginY {
			ticks = append(ticks, Tick{x, PageH - MarginY, x, PageH - MarginY + TickLen})
		}
	}
	return ticks
}

// span is how far a cut line extends, accumulated across the cards that
// share it.
type span struct{ lo, hi float64 }

func addSpan(m map[float64]span, at, lo, hi float64) {
	s, ok := m[at]
	if !ok {
		m[at] = span{lo, hi}
		return
	}
	m[at] = span{min(s.lo, lo), max(s.hi, hi)}
}
