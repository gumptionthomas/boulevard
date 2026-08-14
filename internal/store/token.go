package store

import (
	"context"
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
