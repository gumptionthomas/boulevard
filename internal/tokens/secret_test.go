package tokens

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func TestNewSecretShape(t *testing.T) {
	s, err := NewSecret(rand.Reader)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	if len(s) != SecretLen {
		t.Errorf("len = %d, want %d", len(s), SecretLen)
	}
	for _, r := range s {
		if !strings.ContainsRune(boulevard.CrockfordAlphabet, r) {
			t.Errorf("character %q is not in the Crockford alphabet", r)
		}
	}
}

func TestNewSecretIsUniqueAcrossManyDraws(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := NewSecret(rand.Reader)
		if err != nil {
			t.Fatalf("NewSecret: %v", err)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q after %d draws", s, i)
		}
		seen[s] = true
	}
}

func TestNewSecretIsDeterministicUnderAFixedReader(t *testing.T) {
	src := []byte("boulevard-fixed!")
	a, _ := NewSecret(bytes.NewReader(src))
	b, _ := NewSecret(bytes.NewReader(src))
	if a != b {
		t.Errorf("got %q and %q; generation must be injectable so golden tests are stable", a, b)
	}
}
