package boulevard

import (
	"bytes"
	"strings"
	"testing"
)

func TestRandomBase32LengthAndAlphabet(t *testing.T) {
	s, err := RandomBase32(bytes.NewReader(make([]byte, 16)), 16)
	if err != nil {
		t.Fatalf("RandomBase32: %v", err)
	}
	if len(s) != 26 {
		t.Errorf("len = %d, want 26 (128 bits at 5 bits per char)", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(CrockfordAlphabet, r) {
			t.Errorf("character %q is not in the Crockford alphabet", r)
		}
	}
}

func TestCrockfordAlphabetExcludesAmbiguousLetters(t *testing.T) {
	if len(CrockfordAlphabet) != 32 {
		t.Fatalf("alphabet has %d characters, want 32", len(CrockfordAlphabet))
	}
	for _, bad := range "ILOU" {
		if strings.ContainsRune(CrockfordAlphabet, bad) {
			t.Errorf("alphabet must exclude %q — DESIGN.md §4 requires no ambiguous characters", bad)
		}
	}
}

func TestRandomBase32IsDeterministicForAGivenReader(t *testing.T) {
	src := []byte("0123456789abcdef")
	a, err := RandomBase32(bytes.NewReader(src), 16)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RandomBase32(bytes.NewReader(src), 16)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("same reader bytes produced %q and %q; generation must be injectable for tests", a, b)
	}
}

func TestRandomBase32FailsOnShortReader(t *testing.T) {
	if _, err := RandomBase32(bytes.NewReader([]byte{1, 2, 3}), 16); err == nil {
		t.Error("want error when the reader cannot supply enough entropy")
	}
}
