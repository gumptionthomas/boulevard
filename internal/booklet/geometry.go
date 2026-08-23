package booklet

// All dimensions are PostScript points: 72 pt = 1 inch.
const (
	PageW = 612.0 // US Letter, 8.5 in
	PageH = 792.0 // US Letter, 11 in

	// A business card. Sleeves and holders are commodity-available in
	// exactly this size, and a small card resists being scanned from a
	// passing car — which protects presence as the credential.
	CardW = 252.0 // 3.5 in
	CardH = 144.0 // 2 in

	Cols = 2
	Rows = 5

	GridW = CardW * Cols // 504
	GridH = CardH * Rows // 720

	MarginX = (PageW - GridW) / 2 // 54 pt, 0.75 in
	MarginY = (PageH - GridH) / 2 // 36 pt, 0.5 in

	// Card face.
	BarH      = 19.0 // purpose bar
	CardPadX  = 13.0
	CardQR    = 83.0  // 1.15 in
	TextColX  = 107.5 // from the card's left edge
	HairlineW = 0.4

	// The cover occupies the first four rows of sheet 2; the last two
	// cards sit in row 5. One horizontal cut at CoverH + MarginY frees
	// the cover intact.
	CoverRows = 4
	CoverH    = CardH * CoverRows // 576

	// Browse sign, cut out of the cover.
	SignW  = 302.4 // 4.2 in
	SignH  = 158.4 // 2.2 in
	SignQR = 108.0 // 1.5 in
)

// Copy that must not drift. DESIGN.md §4, as amended.
const (
	CardVerb      = "SCAN TO LEAVE OR TAKE"
	SignVerb      = "Scan to browse the shelf"
	SignSubtitle  = "Anyone, anywhere, anytime. No code needed."
	SignageLine   = "Take something. Leave something. Scan to leave."
	SleeveWarning = "Mount the shelf code in a sleeve. Never laminate or engrave it — a hosting arrangement can end, and it must stay replaceable."
)

type Rect struct{ X, Y, W, H float64 }

// CardRect returns the position of card i (zero-based) within a sheet grid.
func CardRect(i int) Rect {
	col := i % Cols
	row := i / Cols
	return Rect{
		X: MarginX + float64(col)*CardW,
		Y: MarginY + float64(row)*CardH,
		W: CardW,
		H: CardH,
	}
}
