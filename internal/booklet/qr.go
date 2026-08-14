// Package booklet plans and renders the printable token booklet.
package booklet

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gumptionthomas/boulevard/internal/tokens"
	qrcode "github.com/skip2/go-qrcode"
)

// MaxComfortableVersion bounds symbol density. At the card's 83 pt QR, a
// version 8 symbol (49 modules) is about 0.59 mm per module — the point
// where phone scanning in poor light starts to suffer. Anything denser
// means the base URL is too long to print reliably at this size.
const MaxComfortableVersion = 8

// ValidateCardPayload reports whether a card's QR stays at a comfortable
// density for this base URL, before anything is generated.
//
// It is the same refusal drawCard makes, hoisted to where it belongs: the
// base URL is a --base-url problem, and discovering it at render time meant
// a database written, a booklet minted, and no PDF to show for it.
//
// The probe secret is a run of one alphanumeric character, which can never
// encode in fewer bits than a real secret of the same length — so this can
// refuse a payload the renderer would have accepted at the very boundary,
// but it can never accept one the renderer will refuse.
func ValidateCardPayload(baseURL string) error {
	probe := baseURL + "/s/" + strings.Repeat("W", tokens.SecretLen)
	code, err := Encode(probe)
	if err != nil {
		return err
	}
	if !code.TooDense() {
		return nil
	}
	return fmt.Errorf("base URL %q is too long: a card's QR would need version %d, past the comfortable maximum of %d at %.0f pt, and cards would scan badly in poor light. Pass a shorter --base-url",
		baseURL, code.Version, MaxComfortableVersion, CardQR)
}

// Code is a QR symbol as a bitmap of dark modules, quiet zone removed.
type Code struct {
	Modules [][]bool
	Size    int // modules per side
	Version int
}

func (c Code) TooDense() bool { return c.Version > MaxComfortableVersion }

// Encode builds a symbol at ECC level Q (~25% recoverable).
//
// A card lives taped inside a box door through a winter; it gets damp and
// creased. A steward cannot tell a damaged QR from a revoked one, so
// durability is worth the extra modules.
func Encode(payload string) (Code, error) {
	if payload == "" {
		return Code{}, errors.New("qr payload is empty")
	}
	// go-qrcode's "High" is ECC level Q (25% error recovery) — see the
	// package's RecoveryLevel constant doc comments. The constant names do
	// not match the ECC letters.
	q, err := qrcode.New(payload, qrcode.High)
	if err != nil {
		return Code{}, fmt.Errorf("encode qr for %q: %w", payload, err)
	}

	full := q.Bitmap()
	// Bitmap() includes a 4-module quiet zone on every side (confirmed
	// against the version formula 4*version+17 during implementation).
	const quiet = 4
	size := len(full) - 2*quiet
	if size < 21 { // a version 1 symbol is 21 modules
		return Code{}, fmt.Errorf("qr bitmap is %d modules after stripping the quiet zone", size)
	}
	// A QR symbol of version v is always 4v+17 modules on a side. If the
	// library's border ever changed width, or a future substitute library
	// used a different one, size would stop fitting that formula. Catch it
	// here rather than silently truncating into a wrong Version below.
	if (size-17)%4 != 0 {
		return Code{}, fmt.Errorf("qr bitmap size %d does not fit the version formula 4v+17", size)
	}

	mods := make([][]bool, size)
	for y := 0; y < size; y++ {
		row := make([]bool, size)
		copy(row, full[y+quiet][quiet:quiet+size])
		mods[y] = row
	}

	return Code{Modules: mods, Size: size, Version: (size - 17) / 4}, nil
}
