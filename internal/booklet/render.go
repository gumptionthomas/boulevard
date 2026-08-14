package booklet

import (
	"bytes"
	"errors"
	"fmt"
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

// drawCutMarks draws hairlines on the shared card edges plus ticks into the
// margins, so a steward can lay a straightedge across the page. Scissors,
// not a guillotine (DESIGN.md §4).
func drawCutMarks(pdf *fpdf.Fpdf, sheet Sheet) {
	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)

	const tick = 12.0
	for _, c := range sheet.Cards {
		pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "D")
		pdf.Line(c.Rect.X, c.Rect.Y, c.Rect.X-tick, c.Rect.Y)
		pdf.Line(c.Rect.X+c.Rect.W, c.Rect.Y, c.Rect.X+c.Rect.W+tick, c.Rect.Y)
	}
	if sheet.Cover != nil {
		// The single horizontal cut that frees the cover.
		cut := sheet.Cover.Rect.Y + sheet.Cover.Rect.H
		pdf.Line(MarginX-tick, cut, PageW-MarginX+tick, cut)
	}
}
