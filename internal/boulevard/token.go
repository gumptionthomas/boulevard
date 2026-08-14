package boulevard

import "time"

type TokenState string

const (
	TokenPending TokenState = "pending"
	TokenActive  TokenState = "active"
	TokenExpired TokenState = "expired"
	TokenRevoked TokenState = "revoked"
)

// Token is one month's leave/take credential.
//
// Secret is stored in plaintext on purpose (DESIGN.md §4): a lost booklet is
// far likelier than server compromise, and reprinting must reproduce
// identical cards. Never hash it.
type Token struct {
	ID          string
	LibraryID   LibraryID
	Secret      string
	PeriodIndex int
	ValidFrom   Date
	ValidUntil  Date
	State       TokenState
	FirstSeenAt *time.Time
}
