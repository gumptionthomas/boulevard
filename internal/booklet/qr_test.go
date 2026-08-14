package booklet

import (
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func TestEncodeProducesSquareBitmap(t *testing.T) {
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if c.Size <= 0 {
		t.Fatalf("Size = %d, want positive", c.Size)
	}
	if len(c.Modules) != c.Size {
		t.Errorf("bitmap has %d rows, want %d", len(c.Modules), c.Size)
	}
	for i, row := range c.Modules {
		if len(row) != c.Size {
			t.Errorf("row %d has %d columns, want %d", i, len(row), c.Size)
		}
	}
}

func TestSizeMatchesVersionFormula(t *testing.T) {
	// A QR symbol of version v is 4v+17 modules on a side.
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if want := 4*c.Version + 17; c.Size != want {
		t.Errorf("Size = %d, want %d for version %d", c.Size, want, c.Version)
	}
}

func TestQuietZoneIsStripped(t *testing.T) {
	// A stripped symbol always has a dark module at its top-left corner —
	// that is the corner of the finder pattern. With a quiet zone present
	// the corner would be light.
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Modules[0][0] {
		t.Error("top-left module is light; the quiet zone was not stripped")
	}
}

func TestModulesAndVersionMatchLibraryIndependently(t *testing.T) {
	// TestQuietZoneIsStripped only checks one corner pixel, and the finder
	// pattern's diagonal happens to be dark at several nearby offsets
	// (0, 2, 3, 4, 6 of 0-6), so a border stripped 2 modules too wide or
	// too narrow could still land on a dark pixel there and pass. This
	// test instead encodes the same payload directly through the
	// underlying library — bypassing Encode's own quiet-zone constant and
	// its Size/Version derivation entirely — and compares every module
	// plus the library's own VersionNumber field against what Encode
	// returned. It can only pass if the border removed is exactly 4
	// modules on every side and nothing else was disturbed.
	payload := "https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C"

	c, err := Encode(payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	q, err := qrcode.New(payload, qrcode.High)
	if err != nil {
		t.Fatalf("qrcode.New: %v", err)
	}
	full := q.Bitmap()

	if c.Version != q.VersionNumber {
		t.Errorf("Version = %d, want %d (library's own VersionNumber)", c.Version, q.VersionNumber)
	}

	const border = 4
	if want := len(full) - 2*border; c.Size != want {
		t.Fatalf("Size = %d, want %d (library bitmap is %d modules wide, minus a %d-module border on each side)", c.Size, want, len(full), border)
	}
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if want := full[y+border][x+border]; c.Modules[y][x] != want {
				t.Fatalf("Modules[%d][%d] = %v, want %v (library bitmap[%d][%d])", y, x, c.Modules[y][x], want, y+border, x+border)
			}
		}
	}
}

func TestTypicalPayloadStaysComfortable(t *testing.T) {
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if c.TooDense() {
		t.Errorf("version %d is flagged too dense for a typical payload", c.Version)
	}
}

func TestLongBaseURLIsFlaggedTooDense(t *testing.T) {
	long := "https://" + strings.Repeat("verylongsubdomain.", 8) + "example.org"
	c, err := Encode(long + "/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if !c.TooDense() {
		t.Errorf("version %d should be flagged; a long base URL shrinks each module below reliable scanning size", c.Version)
	}
}

func TestEncodeRejectsEmptyPayload(t *testing.T) {
	if _, err := Encode(""); err == nil {
		t.Error("Encode(\"\") succeeded, want error")
	}
}
