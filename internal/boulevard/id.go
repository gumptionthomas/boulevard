package boulevard

import (
	"encoding/base32"
	"fmt"
	"io"
)

// CrockfordAlphabet excludes I, L, O and U so a secret read off paper cannot
// be mistyped into a different valid secret (DESIGN.md §4).
const CrockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var crockford = base32.NewEncoding(CrockfordAlphabet).WithPadding(base32.NoPadding)

// RandomBase32 reads nBytes of entropy and encodes it. The reader is a
// parameter so tests can be deterministic; production passes crypto/rand.Reader.
func RandomBase32(r io.Reader, nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read %d bytes of entropy: %w", nBytes, err)
	}
	return crockford.EncodeToString(buf), nil
}
