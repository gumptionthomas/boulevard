package boulevard

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
)

func TestNewSessionIDShape(t *testing.T) {
	id, err := NewSessionID(rand.Reader)
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if len(id) != 26 {
		t.Errorf("len = %d, want 26 (128 bits at 5 bits per character)", len(id))
	}
	for _, r := range string(id) {
		if !strings.ContainsRune(CrockfordAlphabet, r) {
			t.Errorf("character %q is not in the Crockford alphabet", r)
		}
	}
}

func TestNewSessionIDIsUnique(t *testing.T) {
	seen := make(map[SessionID]bool, 500)
	for i := 0; i < 500; i++ {
		id, err := NewSessionID(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate session id after %d draws", i)
		}
		seen[id] = true
	}
}

func TestNewSessionIDIsDeterministicUnderAFixedReader(t *testing.T) {
	src := []byte("boulevard-fixed!")
	a, err := NewSessionID(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSessionID(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("got %q and %q; generation must be injectable for tests", a, b)
	}
}
