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
//
// **The caller must check the returned LibraryID itself.** A session id is
// an identifier-resolution boundary, like a token secret: it takes no
// LibraryID because it produces one. A session minted at one box grants
// nothing at another, and this method does not know which box is being
// asked about — web.liveSession is what enforces the match today.
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

// LeavesForSession is the per-session leave count (DESIGN.md §4: 3 leaves).
//
// A bare counter, not a set of item ids. §3 forbids linking a left item to
// the session that left it: a left item is public and permanent, so a link
// from it would point at durable data from the other side and survive the
// session sweep — the user record §1 forbids. Takes are the other case and
// are linked, because a take is private and its row dies with the session.
func (s *Store) LeavesForSession(ctx context.Context, sessionID boulevard.SessionID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT leaves_used FROM sessions WHERE id = ?`, string(sessionID)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("read leaves_used: %w", err)
	}
	return n, nil
}

func (s *Store) IncrementLeaves(ctx context.Context, sessionID boulevard.SessionID) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET leaves_used = leaves_used + 1 WHERE id = ?`,
		string(sessionID))
	if err != nil {
		return fmt.Errorf("increment leaves_used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("increment leaves_used: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("session: %w", ErrNotFound)
	}
	return nil
}

// DeleteSession removes one session by id.
//
// Like SessionByID it takes no LibraryID, and for the same reason: the id
// resolves to its own library. **A caller acting on behalf of a particular
// library must load the session first and check its LibraryID**, or it can
// delete a session belonging to a neighbouring shelf.
func (s *Store) DeleteSession(ctx context.Context, id boulevard.SessionID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
