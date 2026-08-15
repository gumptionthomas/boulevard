package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func (s *Store) CreateSession(ctx context.Context, sess boulevard.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, library_id, token_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		string(sess.ID), string(sess.LibraryID), sess.TokenID,
		sess.CreatedAt.UTC().Format(time.RFC3339),
		sess.ExpiresAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionByID loads a live session. An expired row is reported as ErrNotFound
// rather than returned: expiry is checked on read, so there is no sweeper and
// no window in which a stale session is honoured.
func (s *Store) SessionByID(ctx context.Context, id boulevard.SessionID, now time.Time) (boulevard.Session, error) {
	var (
		sess             boulevard.Session
		libID            string
		created, expires string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, library_id, token_id, created_at, expires_at FROM sessions WHERE id = ?`,
		string(id)).
		Scan(&sess.ID, &libID, &sess.TokenID, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Session{}, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return boulevard.Session{}, fmt.Errorf("look up session: %w", err)
	}

	sess.LibraryID = boulevard.LibraryID(libID)
	if sess.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return boulevard.Session{}, fmt.Errorf("session created_at: %w", err)
	}
	if sess.ExpiresAt, err = time.Parse(time.RFC3339, expires); err != nil {
		return boulevard.Session{}, fmt.Errorf("session expires_at: %w", err)
	}
	if !now.Before(sess.ExpiresAt) {
		return boulevard.Session{}, fmt.Errorf("session expired: %w", ErrNotFound)
	}
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, id boulevard.SessionID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
