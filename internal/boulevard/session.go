package boulevard

import (
	"fmt"
	"io"
	"time"
)

// SessionTTL is how long a scan's grant lasts (DESIGN.md §4).
//
// Twenty-four hours is deliberate: nobody wants to compose a thoughtful note
// standing outside a box in the rain. Scan on your walk, write at your
// kitchen table. The physical act stays mandatory; the typing does not.
const SessionTTL = 24 * time.Hour

// SessionID is the opaque value carried in the cookie.
//
// It is not signed. The session row in SQLite is the source of truth, so the
// lookup is already the check; a signature would add a key to store, rotate
// and lose while defending against nothing. A key outside the database would
// also break §10's promise that ejecting a library is a file copy.
type SessionID string

// Session is one 24-hour grant, minted by one scan of one token.
type Session struct {
	ID        SessionID
	LibraryID LibraryID
	TokenID   string // which card minted it — DESIGN.md §4
	CreatedAt time.Time
	ExpiresAt time.Time
}

func NewSessionID(r io.Reader) (SessionID, error) {
	s, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new session id: %w", err)
	}
	return SessionID(s), nil
}
