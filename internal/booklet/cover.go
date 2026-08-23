package booklet

import (
	"fmt"
	"strings"

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

// installSteps carries no step numbers: they are generated at render time,
// so the list can be reordered or added to without editing every string.
//
// Nothing here names a Little Free Library. A Boulevard may be a sandwich
// board, a garage door, or a fence — "inside the box door" and "the hinge"
// asked a steward to have things they may not have. Nor does any step claim
// a scan distance: the shelf code is 1.5in today and readable only up
// close, so "from the sidewalk" would be a promise the paper cannot keep.
// Milestone 5.5 makes it true, and this string changes with it.
var installSteps = []string{
	"Cut the cards apart along the hairlines. Keep them in order.",
	"Put the current month's card at the shelf, where someone has to come close to scan it.",
	"On the first of each month, swap in the next card.",
	"While you are there, look at the shelf and what is on it.",
	"Mount the shelf code where people will see it.",
}

func drawCover(pdf *fpdf.Fpdf, c Cover) error {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "F")

	left := c.Rect.X + 28

	// Title block. LibraryName and LocationLabel are host-supplied and may
	// carry accents or punctuation outside ASCII, so both must go through
	// toCP1252 before Text() — see render.go's toCP1252 doc comment.
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Times", "B", 20)
	pdf.Text(left, c.Rect.Y+44, toCP1252(c.LibraryName))

	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(110, 107, 98)
	pdf.Text(left, c.Rect.Y+64, toCP1252(c.LocationLabel))

	// The browse sign is the one artifact meant to be mounted permanently
	// and read by strangers — refuse to produce it wrong rather than print
	// a library name running past its border, the same stance the base URL
	// gets before any PDF is written at all.
	if err := drawBrowseSign(pdf, SignRect(c.Rect), c.SignPayload, c.LibraryName); err != nil {
		return fmt.Errorf("shelf code: %w", err)
	}

	// Instructions.
	instrY := SignRect(c.Rect).Y + SignH + 40
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.Text(left, instrY, "Setting up")

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 58, 52)
	for i, step := range installSteps {
		pdf.Text(left, instrY+18+float64(i)*15, fmt.Sprintf("%d.  %s", i+1, step))
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
	pdf.MultiCell(c.Rect.W-56, 11, toCP1252(SleeveWarning), "", "L", false)

	// Signage line, set apart.
	pdf.SetFont("Times", "", 13)
	pdf.SetTextColor(inkR, inkG, inkB)
	signage := toCP1252(SignageLine)
	sw := pdf.GetStringWidth(signage)
	pdf.Text(c.Rect.X+(c.Rect.W-sw)/2, c.Rect.Y+c.Rect.H-52, signage)

	// Footer: the AGPL source offer, per DESIGN.md §2. The middle-dot
	// separator is itself non-ASCII, so the whole line goes through
	// toCP1252 even though BuildLine and SourceURL are expected to be
	// plain ASCII.
	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(140, 137, 128)
	pdf.Text(left, c.Rect.Y+c.Rect.H-16, toCP1252(c.BuildLine+"  ·  source: "+c.SourceURL))
	pdf.SetTextColor(inkR, inkG, inkB)
	return nil
}

// signNameMaxLines bounds how many lines the browse sign gives the library
// name. Two lines fit the box with room to spare (see drawBrowseSign); a
// name that still doesn't fit in two is a generation-time error rather than
// text quietly drawn past the sign's border.
const signNameMaxLines = 2

// signTextColumn is where the sign's text starts and how much width it has:
// from just right of the QR to the sign's own right border. Both
// drawBrowseSign and ValidateSignName read the geometry from here, so the
// pre-flight check and the render-time backstop can never measure against
// different widths.
func signTextColumn(r Rect) (x, w float64) {
	x = r.X + 20 + SignQR + 20
	return x, r.X + r.W - x
}

// signNameFont selects the face the browse sign sets the library name in.
// Measuring against any other face would measure the wrong thing.
func signNameFont(pdf *fpdf.Fpdf) { pdf.SetFont("Times", "B", 14) }

// ValidateSignName reports whether name fits the browse sign's two-line
// budget, measured against a throwaway document rather than the one being
// rendered.
//
// This exists so the CLI can refuse a too-long name as a --name validation
// error, before the DNS check, the confirmation prompt, and — crucially —
// before a library and twelve tokens are committed to the database. The
// same check still runs inside drawBrowseSign as a backstop for any caller
// that skips this one.
func ValidateSignName(name string) error {
	pdf := fpdf.New("P", "pt", "Letter", "")
	signNameFont(pdf)
	_, w := signTextColumn(Rect{W: SignW})

	lines, err := wrapToWidth(pdf, name, w)
	if err != nil {
		return err
	}
	if len(lines) <= signNameMaxLines {
		return nil
	}
	return fmt.Errorf("%q is too long for the shelf code (about %d characters fit). Pass a shorter --name",
		name, signNameBudget(pdf, name, w))
}

// signNameBudget is the longest prefix of name, in characters, that still
// fits the sign. Reporting a real measurement beats quoting a constant: the
// budget depends on the letters, since "MMM" is far wider than "lll" at the
// same count.
func signNameBudget(pdf *fpdf.Fpdf, name string, w float64) int {
	runes := []rune(name)
	lo, hi := 0, len(runes) // lo always fits, hi never does
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		lines, err := wrapToWidth(pdf, string(runes[:mid]), w)
		if err != nil || len(lines) > signNameMaxLines {
			hi = mid
			continue
		}
		lo = mid
	}
	return lo
}

// drawBrowseSign paints the permanent, mounted artifact: light, landscape,
// undated — deliberately unlike the dark, dated monthly card. Two QR codes
// that looked alike would confuse a visitor about which does what.
func drawBrowseSign(pdf *fpdf.Fpdf, r Rect, payload, libraryName string) error {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(r.X, r.Y, r.W, r.H, "F")

	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)
	pdf.Rect(r.X, r.Y, r.W, r.H, "D")

	code, err := Encode(payload)
	if err != nil {
		return fmt.Errorf("shelf code qr: %w", err)
	}
	drawQR(pdf, code, r.X+20, r.Y+(r.H-SignQR)/2, SignQR)

	// textW is the true available width for every text line in the sign:
	// from the text column to the sign's own right border. Nothing drawn
	// here may exceed it without running past the border.
	tx, textW := signTextColumn(r)

	pdf.SetTextColor(inkR, inkG, inkB)
	signNameFont(pdf)
	// The name wraps to at most signNameMaxLines lines at the width the sign
	// actually has. A name that still doesn't fit is refused here rather
	// than drawn past the border — a steward needs to know that at
	// generation time, not after the sign is screwed to the box. The block
	// is anchored so its LAST line always sits at baseline r.Y+56: a
	// one-line name sits there directly, a two-line name grows upward from
	// it, so SignVerb and the subtitle below never have to move.
	const nameLineH = 16.0
	nameLines, err := wrapToWidth(pdf, libraryName, textW)
	if err != nil {
		return err
	}
	// The caller (drawCover) already prefixes "shelf code: ", so this must
	// not say it again.
	if len(nameLines) > signNameMaxLines {
		return fmt.Errorf("library name %q needs %d lines at %.1fpt of width, want at most %d — call ValidateSignName before generating",
			libraryName, len(nameLines), textW, signNameMaxLines)
	}
	nameTop := r.Y + 56 - float64(len(nameLines)-1)*nameLineH
	for i, line := range nameLines {
		pdf.Text(tx, nameTop+float64(i)*nameLineH, line)
	}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.Text(tx, r.Y+78, toCP1252(SignVerb))

	// SignSubtitle breaks at its sentence boundary — "...anytime." / "No
	// code needed." — which is how the original design mockup set it, and
	// reads better on a public sign than a purely width-driven wrap (which
	// would orphan "needed." on its own line at this width). SignSubtitle
	// stays one verbatim constant; the split happens here at render time,
	// not as a second constant, so the spec-mandated string is never itself
	// divided. If a future copy change removes the sentence boundary, or
	// either half no longer fits textW, this falls back to the same
	// width-driven wrap used everywhere else on the cover.
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(100, 97, 89)
	const subLineH = 10.0
	subTop := r.Y + 86
	if first, rest, ok := splitAtSentence(SignSubtitle); ok {
		encFirst, encRest := toCP1252(first), toCP1252(rest)
		if pdf.GetStringWidth(encFirst) <= textW && pdf.GetStringWidth(encRest) <= textW {
			pdf.Text(tx, subTop+8, encFirst)
			pdf.Text(tx, subTop+8+subLineH, encRest)
			pdf.SetTextColor(inkR, inkG, inkB)
			return nil
		}
	}
	pdf.SetXY(tx, subTop)
	pdf.MultiCell(textW, subLineH, toCP1252(SignSubtitle), "", "L", false)
	pdf.SetTextColor(inkR, inkG, inkB)
	return nil
}

// wrapToWidth wraps s, translated for the font currently selected on pdf,
// into lines no wider than w. The returned lines are already
// cp1252-encoded — callers must draw them as-is, not translate them again.
func wrapToWidth(pdf *fpdf.Fpdf, s string, w float64) ([]string, error) {
	if w <= 0 {
		return nil, fmt.Errorf("wrap width %.2f is not positive", w)
	}
	raw := pdf.SplitLines([]byte(toCP1252(s)), w)
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = string(l)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines, nil
}

// splitAtSentence splits s after its first ". " into (sentence, rest, true).
// It reports false if s has no such boundary, so callers can fall back to a
// width-driven wrap instead.
func splitAtSentence(s string) (first, rest string, ok bool) {
	idx := strings.Index(s, ". ")
	if idx < 0 {
		return "", "", false
	}
	return s[:idx+1], s[idx+2:], true
}
