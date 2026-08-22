package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// InsertTokens writes a whole booklet's worth of tokens in one transaction.
// A partial booklet is never useful, so the batch is all-or-nothing.
func (s *Store) InsertTokens(ctx context.Context, id boulevard.LibraryID, toks []boulevard.Token) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token insert: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO tokens (id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`)
	if err != nil {
		return fmt.Errorf("prepare token insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, tok := range toks {
		if tok.LibraryID != id {
			return fmt.Errorf("token %d belongs to library %q, not %q", tok.PeriodIndex, tok.LibraryID, id)
		}
		if _, err := stmt.ExecContext(ctx,
			tok.ID, string(id), tok.Secret, tok.PeriodIndex,
			tok.ValidFrom.String(), tok.ValidUntil.String(), string(tok.State), now,
		); err != nil {
			return fmt.Errorf("insert token for period %d: %w", tok.PeriodIndex, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tokens: %w", err)
	}
	return nil
}

func (s *Store) TokensForLibrary(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, secret, period_index, valid_from, valid_until, state, first_seen_at
		   FROM tokens WHERE library_id = ? ORDER BY period_index`, string(id))
	if err != nil {
		return nil, fmt.Errorf("list tokens for library %q: %w", id, err)
	}
	defer rows.Close()

	var out []boulevard.Token
	for rows.Next() {
		var (
			tok       boulevard.Token
			from, til string
			state     string
			seen      *string
		)
		if err := rows.Scan(&tok.ID, &tok.Secret, &tok.PeriodIndex, &from, &til, &state, &seen); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		if tok.ValidFrom, err = boulevard.ParseDate(from); err != nil {
			return nil, fmt.Errorf("token %s valid_from: %w", tok.ID, err)
		}
		if tok.ValidUntil, err = boulevard.ParseDate(til); err != nil {
			return nil, fmt.Errorf("token %s valid_until: %w", tok.ID, err)
		}
		tok.LibraryID = id
		tok.State = boulevard.TokenState(state)
		if seen != nil {
			t, err := time.Parse(time.RFC3339, *seen)
			if err != nil {
				return nil, fmt.Errorf("token %s first_seen_at: %w", tok.ID, err)
			}
			tok.FirstSeenAt = &t
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// TokenBySecret resolves a scanned secret to its token.
//
// This takes no LibraryID, and that is deliberate rather than an oversight.
// Secrets are unique host-wide (DESIGN.md §10) precisely so a token
// identifies its own library; this is an identifier-resolution boundary,
// like LibraryBySlug. Everything downstream stays library-scoped.
func (s *Store) TokenBySecret(ctx context.Context, secret string) (boulevard.Token, error) {
	var (
		tok       boulevard.Token
		libID     string
		from, til string
		state     string
		seen      *string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at
		   FROM tokens WHERE secret = ?`, secret).
		Scan(&tok.ID, &libID, &tok.Secret, &tok.PeriodIndex, &from, &til, &state, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Token{}, fmt.Errorf("token by secret: %w", ErrNotFound)
	}
	if err != nil {
		return boulevard.Token{}, fmt.Errorf("look up token by secret: %w", err)
	}

	tok.LibraryID = boulevard.LibraryID(libID)
	tok.State = boulevard.TokenState(state)
	if tok.ValidFrom, err = boulevard.ParseDate(from); err != nil {
		return boulevard.Token{}, fmt.Errorf("token %s valid_from: %w", tok.ID, err)
	}
	if tok.ValidUntil, err = boulevard.ParseDate(til); err != nil {
		return boulevard.Token{}, fmt.Errorf("token %s valid_until: %w", tok.ID, err)
	}
	if seen != nil {
		t, err := time.Parse(time.RFC3339, *seen)
		if err != nil {
			return boulevard.Token{}, fmt.Errorf("token %s first_seen_at: %w", tok.ID, err)
		}
		tok.FirstSeenAt = &t
	}
	return tok, nil
}

// RecordScan stamps first_seen_at and advances the active card.
//
// Activation only ever moves forward (spec §7). An older card scanned
// inside its grace window is still recorded as seen and still grants its
// caller a session, but it must not displace a newer active card — a stray
// card found in a drawer cannot be allowed to rewind the steward's sense of
// which card is in the door.
//
// Both updates happen in one transaction: a stamped scan that failed to
// advance the state would misreport the shelf.
func (s *Store) RecordScan(ctx context.Context, id boulevard.LibraryID, tok boulevard.Token, now time.Time) error {
	if tok.LibraryID != id {
		return fmt.Errorf("token %s belongs to library %q, not %q", tok.ID, tok.LibraryID, id)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record scan: %w", err)
	}
	defer tx.Rollback()

	if tok.FirstSeenAt == nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tokens SET first_seen_at = ? WHERE id = ? AND first_seen_at IS NULL`,
			now.UTC().Format(time.RFC3339), tok.ID); err != nil {
			return fmt.Errorf("stamp first_seen_at on %s: %w", tok.ID, err)
		}
	}

	var activePeriod int
	err = tx.QueryRowContext(ctx,
		`SELECT period_index FROM tokens WHERE library_id = ? AND state = ?`,
		string(id), string(boulevard.TokenActive)).Scan(&activePeriod)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Nothing active yet, so any token may take the slot. 0 works as
		// the sentinel only because period_index is 1-based (DESIGN.md §4:
		// "period_index INT -- 1..12"), which makes every real card strictly
		// greater than it. A 0-based index would silently refuse to activate
		// the first card of a booklet.
		activePeriod = 0
	case err != nil:
		return fmt.Errorf("find active token for %q: %w", id, err)
	}

	if tok.PeriodIndex > activePeriod {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tokens SET state = ? WHERE library_id = ? AND state = ?`,
			string(boulevard.TokenExpired), string(id), string(boulevard.TokenActive)); err != nil {
			return fmt.Errorf("expire previous active token: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tokens SET state = ? WHERE id = ?`,
			string(boulevard.TokenActive), tok.ID); err != nil {
			return fmt.Errorf("activate token %s: %w", tok.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record scan: %w", err)
	}
	return nil
}

// ForceActivateToken promotes a pending card, for the steward who swapped
// the card early (DESIGN.md §4).
//
// It moves valid_from to today, and that is the whole point rather than a
// side effect. tokens.Validate reads state only for "revoked"; everything
// else is the date window. Setting state = active on a card whose period
// has not started leaves it answering NotYet — the command would report
// success and change nothing a scanner can see. Inside the 7-day grace the
// command is redundant anyway, because the card already scans, so the only
// case that reaches here is one where a date has to move.
//
// valid_until is deliberately not moved: the card ends when it was always
// going to end. The printed card will now disagree with the database, which
// is accepted — the steward has physically put that card in the door, and
// the month name is how they identify it.
func (s *Store) ForceActivateToken(ctx context.Context, id boulevard.LibraryID, periodIndex int, today boulevard.Date) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin force-activate: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) != boulevard.TokenPending {
		return fmt.Errorf("period %d is %s: %w", periodIndex, state, ErrNotPending)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ? WHERE library_id = ? AND state = ?`,
		string(boulevard.TokenExpired), string(id), string(boulevard.TokenActive)); err != nil {
		return fmt.Errorf("expire the previous active token: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ?, valid_from = ? WHERE library_id = ? AND period_index = ?`,
		string(boulevard.TokenActive), today.String(), string(id), periodIndex); err != nil {
		return fmt.Errorf("activate period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit force-activate: %w", err)
	}
	return nil
}

// ExtendToken pushes the active card's end date to the last day of the
// month after the one it currently ends in, and returns that date so the
// caller can name it. For the steward whose booklet is lost and whose
// replacement is not printed yet (DESIGN.md §4).
//
// Later periods are deliberately untouched. Cascading the shift would keep
// exactly one card valid at a time but would make every unswapped printed
// card disagree with the database — the card reading "September" would
// carry October's period. Two cards valid at once is the smaller problem,
// and one this design already accepts: the 7-day grace on both ends means
// adjacent cards overlap by fourteen days regardless.
func (s *Store) ExtendToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) (boulevard.Date, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("begin extend: %w", err)
	}
	defer tx.Rollback()

	var state, until string
	err = tx.QueryRowContext(ctx,
		`SELECT state, valid_until FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Date{}, ErrNotFound
	}
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) != boulevard.TokenActive {
		return boulevard.Date{}, fmt.Errorf("period %d is %s: %w", periodIndex, state, ErrNotActive)
	}

	cur, err := boulevard.ParseDate(until)
	if err != nil {
		return boulevard.Date{}, fmt.Errorf("parse valid_until %q: %w", until, err)
	}
	next := cur.NextMonth().LastOfMonth()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET valid_until = ? WHERE library_id = ? AND period_index = ?`,
		next.String(), string(id), periodIndex); err != nil {
		return boulevard.Date{}, fmt.Errorf("extend period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return boulevard.Date{}, fmt.Errorf("commit extend: %w", err)
	}
	return next, nil
}

// RevokeToken burns one card's secret, for a sheet that was stolen or
// photographed (DESIGN.md §4). "revoked" is the one state tokens.Validate
// reads, and §4 requires a revoked secret to produce a response
// byte-identical to an unknown one — already true, and pinned by
// TestScanRevokedIsIndistinguishableFromUnknown.
//
// There is no un-revoke. The way forward is force-activating the next card
// or rotating.
func (s *Store) RevokeToken(ctx context.Context, id boulevard.LibraryID, periodIndex int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM tokens WHERE library_id = ? AND period_index = ?`,
		string(id), periodIndex).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read token for period %d: %w", periodIndex, err)
	}
	if boulevard.TokenState(state) == boulevard.TokenRevoked {
		return ErrAlreadyRevoked
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tokens SET state = ? WHERE library_id = ? AND period_index = ?`,
		string(boulevard.TokenRevoked), string(id), periodIndex); err != nil {
		return fmt.Errorf("revoke period %d: %w", periodIndex, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke: %w", err)
	}
	return nil
}
