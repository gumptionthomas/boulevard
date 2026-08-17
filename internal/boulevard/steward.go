package boulevard

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
)

// NewStewardKey generates the steward's credential (DESIGN.md §6, §8).
//
// It is generated, never chosen. §8 already implies this — "prints the
// steward password once" is the behaviour of something generated — and the
// distinction settles two things at once. A chosen password would need a
// real KDF, and therefore a dependency this project does not carry, because
// chosen passwords are low-entropy and reused across sites so a leak harms
// the steward elsewhere. A generated 128-bit secret has neither problem.
//
// Same alphabet and length as a token secret, for the reason §4 gives: a
// value read off paper must not be mistypeable into a different valid one.
func NewStewardKey(r io.Reader) (string, error) {
	k, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new steward key: %w", err)
	}
	return k, nil
}

// HashStewardKey is what gets stored. Only the hash is kept.
//
// This is not the same decision as §4's deliberately plaintext token
// secrets, and the difference is worth stating because the two rules
// otherwise look contradictory. A token secret stays recoverable because
// reprinting must always work — a lost booklet is likelier than server
// compromise. A steward key has no artifact to reprint: losing it means
// generating another, which costs nothing, so the recoverable form is not
// worth keeping.
//
// Plain SHA-256 with no salt is correct for a 128-bit random input. Salting
// defends against precomputation across many low-entropy secrets; there is
// no precomputing a 128-bit random value.
func HashStewardKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// StewardKeyMatches compares in constant time.
//
// An empty hash is the "no key set" state and must match nothing at all,
// including the empty key a blank form would submit — otherwise a fresh
// install would admit anyone who pressed the button.
func StewardKeyMatches(hash, key string) bool {
	if hash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashStewardKey(key))) == 1
}
