package booklet

import (
	"fmt"

	"github.com/go-pdf/fpdf"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// drawCard paints one monthly card: a dark purpose bar over a month-forward
// body with the QR on the left.
//
// The bar earns its space twice — it tells a visitor what scanning does, and
// it makes the card unmistakable against the light, landscape browse sign.
func drawCard(pdf *fpdf.Fpdf, c PlacedCard) error {
	code, err := Encode(c.Payload)
	if err != nil {
		return err
	}
	if code.TooDense() {
		return fmt.Errorf("qr version %d exceeds the comfortable maximum of %d — the base URL is too long to print reliably at %.0f pt",
			code.Version, MaxComfortableVersion, CardQR)
	}

	// Paper, so the card reads as an object rather than a hole in the page.
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "F")

	// Purpose bar.
	pdf.SetFillColor(inkR, inkG, inkB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, BarH, "F")
	pdf.SetFont("Helvetica", "B", 7.5)
	pdf.SetTextColor(paperR, paperG, paperB)
	const tracking = 0.9
	bw := trackedWidth(pdf, CardVerb, tracking)
	drawTracked(pdf, c.Rect.X+(c.Rect.W-bw)/2, c.Rect.Y+BarH-6.5, CardVerb, tracking)

	// QR, vertically centred in the body below the bar.
	bodyY := c.Rect.Y + BarH
	bodyH := c.Rect.H - BarH
	drawQR(pdf, code, c.Rect.X+CardPadX, bodyY+(bodyH-CardQR)/2, CardQR)

	// Text column.
	pdf.SetTextColor(inkR, inkG, inkB)
	tx := c.Rect.X + TextColX

	pdf.SetFont("Times", "B", 15)
	pdf.Text(tx, bodyY+38, toCP1252(c.Month))

	pdf.SetFont("Times", "", 9.5)
	pdf.SetTextColor(90, 88, 80)
	pdf.Text(tx, bodyY+52, fmt.Sprintf("%d", c.Year))

	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)
	pdf.Line(tx, bodyY+60, c.Rect.X+c.Rect.W-CardPadX, bodyY+60)

	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(60, 58, 52)
	// En dash: core fonts carry no encoding of their own, so this must go
	// through toCP1252 or it prints as mojibake — confirmed by rendering
	// and eyeballing the golden PDF during development.
	pdf.Text(tx, bodyY+74, toCP1252(fmt.Sprintf("%s – %s", shortDate(c.From), shortDate(c.Until))))

	pdf.SetFont("Helvetica", "", 6.5)
	pdf.SetTextColor(120, 117, 108)
	drawTracked(pdf, tx, bodyY+88, fmt.Sprintf("CARD %d OF %d", c.Index, c.Of), 0.5)

	pdf.SetTextColor(inkR, inkG, inkB)
	return nil
}

// shortDate renders "Aug 14" for the card's date range.
func shortDate(d boulevard.Date) string {
	return fmt.Sprintf("%s %d", d.Month.String()[:3], d.Day)
}
