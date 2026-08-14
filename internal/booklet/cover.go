package booklet

import (
	"github.com/go-pdf/fpdf"
)

// SignRect centres the browse sign horizontally within the cover, seated
// below the title block.
func SignRect(cover Rect) Rect {
	return Rect{
		X: cover.X + (cover.W-SignW)/2,
		Y: cover.Y + 94,
		W: SignW,
		H: SignH,
	}
}

var installSteps = []string{
	"1.  Cut the cards apart along the hairlines. Keep them in order.",
	"2.  Tape the current month's card inside the box door.",
	"3.  On the first of each month, swap in the next card.",
	"4.  While you are there, look at the hinge, the sign, and the shelf.",
	"5.  Mount the browse sign where it can be read from the sidewalk.",
}

func drawCover(pdf *fpdf.Fpdf, c Cover) {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "F")

	left := c.Rect.X + 28

	// Title block. LibraryName and LocationLabel are host-supplied and may
	// carry accents or punctuation outside ASCII, so both must go through
	// toCP1252 before Text() — see render.go's toCP1252 doc comment.
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Times", "B", 20)
	pdf.Text(left, c.Rect.Y+44, toCP1252(pdf, c.LibraryName))

	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(110, 107, 98)
	pdf.Text(left, c.Rect.Y+64, toCP1252(pdf, c.LocationLabel))

	drawBrowseSign(pdf, SignRect(c.Rect), c.SignPayload, c.LibraryName)

	// Instructions.
	instrY := SignRect(c.Rect).Y + SignH + 40
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.Text(left, instrY, "Setting up")

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 58, 52)
	for i, step := range installSteps {
		pdf.Text(left, instrY+18+float64(i)*15, step)
	}

	// The sleeve warning. DESIGN.md: a printed sign outlives the hosting
	// arrangement that made its URL work. The em dash makes this non-ASCII,
	// so it must be translated before MultiCell — otherwise fpdf both
	// mis-measures the wrap width (core-font width lookup is per byte) and
	// prints mojibake for the dash.
	warnY := instrY + 18 + float64(len(installSteps))*15 + 14
	pdf.SetFont("Helvetica", "I", 8.5)
	pdf.SetTextColor(110, 107, 98)
	pdf.SetXY(left, warnY)
	pdf.MultiCell(c.Rect.W-56, 11, toCP1252(pdf, SleeveWarning), "", "L", false)

	// Signage line, set apart.
	pdf.SetFont("Times", "", 13)
	pdf.SetTextColor(inkR, inkG, inkB)
	signage := toCP1252(pdf, SignageLine)
	sw := pdf.GetStringWidth(signage)
	pdf.Text(c.Rect.X+(c.Rect.W-sw)/2, c.Rect.Y+c.Rect.H-52, signage)

	// Footer: the AGPL source offer, per DESIGN.md §2. The middle-dot
	// separator is itself non-ASCII, so the whole line goes through
	// toCP1252 even though BuildLine and SourceURL are expected to be
	// plain ASCII.
	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(140, 137, 128)
	pdf.Text(left, c.Rect.Y+c.Rect.H-16, toCP1252(pdf, c.BuildLine+"  ·  source: "+c.SourceURL))
	pdf.SetTextColor(inkR, inkG, inkB)
}

// drawBrowseSign paints the permanent, mounted artifact: light, landscape,
// undated — deliberately unlike the dark, dated monthly card. Two QR codes
// that looked alike would confuse a visitor about which does what.
func drawBrowseSign(pdf *fpdf.Fpdf, r Rect, payload, libraryName string) {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(r.X, r.Y, r.W, r.H, "F")

	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)
	pdf.Rect(r.X, r.Y, r.W, r.H, "D")

	code, err := Encode(payload)
	if err != nil {
		return // a plan that reached rendering already validated its payloads
	}
	qrX := r.X + 20
	drawQR(pdf, code, qrX, r.Y+(r.H-SignQR)/2, SignQR)

	tx := qrX + SignQR + 20
	// textW is the true available width for every text line in the sign:
	// from the text column to the sign's own right border. Nothing drawn
	// here may exceed it without running past the border.
	textW := r.X + r.W - tx

	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Times", "B", 14)
	pdf.Text(tx, r.Y+56, toCP1252(pdf, libraryName))

	pdf.SetFont("Helvetica", "B", 9)
	pdf.Text(tx, r.Y+78, toCP1252(pdf, SignVerb))

	// SignSubtitle is longer than fits on one line at the smallest
	// comfortable size for a sign meant to be read at the box (8pt), so it
	// wraps via MultiCell rather than shrinking further. The break point is
	// whatever textW dictates — SignSubtitle is copy mandated verbatim
	// elsewhere, so it is never split by hand.
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(100, 97, 89)
	pdf.SetXY(tx, r.Y+86)
	pdf.MultiCell(textW, 10, toCP1252(pdf, SignSubtitle), "", "L", false)
	pdf.SetTextColor(inkR, inkG, inkB)
}
