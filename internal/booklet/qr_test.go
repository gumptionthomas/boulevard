package booklet

import (
	"strings"
	"testing"
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
