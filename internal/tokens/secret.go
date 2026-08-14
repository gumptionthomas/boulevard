// Package tokens generates leave/take token secrets and assigns them
// explicit calendar periods.
package tokens

import (
	"fmt"
	"io"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// SecretLen is the encoded length of 128 bits at 5 bits per character.
const SecretLen = 26

func NewSecret(r io.Reader) (string, error) {
	s, err := boulevard.RandomBase32(r, boulevard.EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new token secret: %w", err)
	}
	return s, nil
}
