package boulevard

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewStewardKeyIsCrockfordAndFullEntropy(t *testing.T) {
	key, err := NewStewardKey(bytes.NewReader(make([]byte, EntropyBytes)))
	if err != nil {
		t.Fatalf("NewStewardKey: %v", err)
	}
	// 16 bytes of base32 with no padding is 26 characters.
	if len(key) != 26 {
		t.Errorf("len = %d, want 26", len(key))
	}
	for _, r := range key {
		if !strings.ContainsRune(CrockfordAlphabet, r) {
			t.Errorf("key contains %q, which is outside the Crockford alphabet", r)
		}
	}
}

func TestStewardKeyMatchesOnlyTheRightKey(t *testing.T) {
	hash := HashStewardKey("K7Q93ZDMXR148PVBTN5A6CQXW2")

	if !StewardKeyMatches(hash, "K7Q93ZDMXR148PVBTN5A6CQXW2") {
		t.Error("the correct key did not match its own hash")
	}
	if StewardKeyMatches(hash, "K7Q93ZDMXR148PVBTN5A6CQXW3") {
		t.Error("a different key matched")
	}
	if StewardKeyMatches(hash, "") {
		t.Error("the empty key matched")
	}
}

// An empty hash is the "no key set" state. Nothing may authenticate against
// it — including the empty string, which is what a form submitted with no
// value would send.
func TestEmptyHashMatchesNothing(t *testing.T) {
	for _, key := range []string{"", "K7Q93ZDMXR148PVBTN5A6CQXW2"} {
		if StewardKeyMatches("", key) {
			t.Errorf("empty hash matched %q", key)
		}
	}
}
