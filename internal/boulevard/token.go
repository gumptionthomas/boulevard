package boulevard

import (
	"strconv"
	"time"
)

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

	// PrintedFrom is the period this card was minted with, and it is what
	// names the card — "October 2026" — on every surface that identifies
	// one. It is set once, at mint, and never modified.
	//
	// It exists because ValidFrom cannot do the job: force-activate
	// rewrites ValidFrom, so a card printed October showed up on the desk
	// as August the moment a steward put it in the door early — including
	// on the confirmation that asks whether to discard secrets while naming
	// the card it will keep. DESIGN.md §4 accepts that force-activate makes
	// the printed card disagree with the database, and its whole reason is
	// that the month name keeps the card in the steward's hand
	// identifiable.
	//
	// ValidUntil is no refuge either: extend moves that one by design.
	// Neither endpoint survives both operations, which is why this is
	// stored rather than derived.
	PrintedFrom Date
}

// Label names the card the way it is printed on the card itself, which is
// how a steward tells which one they are holding.
func (t Token) Label() string {
	return t.PrintedFrom.MonthName() + " " + strconv.Itoa(t.PrintedFrom.Year)
}
