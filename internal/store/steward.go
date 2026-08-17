package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// SetStewardKeyHash stores the hash of a freshly generated key, replacing
// any previous one. Replacing is the whole reset story: a lost key is not
// recoverable, and generating another costs nothing.
func (s *Store) SetStewardKeyHash(ctx context.Context, id boulevard.LibraryID, hash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET steward_key_hash = ? WHERE id = ?`, hash, string(id))
	if err != nil {
		return fmt.Errorf("set steward key for %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set steward key for %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("library %q: %w", id, ErrNotFound)
	}
	return nil
}

// stewardKeyHash reads the stored hash. Unexported: nothing outside this
// file has a reason to hold it, and it is deliberately absent from
// boulevard.Library so that UpdateLibrary cannot blank it.
func (s *Store) stewardKeyHash(ctx context.Context, id boulevard.LibraryID) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT steward_key_hash FROM libraries WHERE id = ?`, string(id)).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("library %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read steward key for %q: %w", id, err)
	}
	return hash, nil
}

// StewardKeyIsSet reports whether the admin is armed at all. An empty hash
// means no key has ever been generated, and every steward route 404s.
func (s *Store) StewardKeyIsSet(ctx context.Context, id boulevard.LibraryID) (bool, error) {
	hash, err := s.stewardKeyHash(ctx, id)
	if err != nil {
		return false, err
	}
	return hash != "", nil
}

// VerifyStewardKey compares in constant time. An unset key verifies nothing.
func (s *Store) VerifyStewardKey(ctx context.Context, id boulevard.LibraryID, key string) (bool, error) {
	hash, err := s.stewardKeyHash(ctx, id)
	if err != nil {
		return false, err
	}
	return boulevard.StewardKeyMatches(hash, key), nil
}

func (s *Store) CreateStewardSession(ctx context.Context, sess boulevard.StewardSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO steward_sessions (id, library_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		sess.ID, string(sess.LibraryID),
		sess.CreatedAt.UTC().Format(time.RFC3339),
		sess.ExpiresAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create steward session: %w", err)
	}
	return nil
}

// StewardSessionByID is a resolution boundary: it takes no LibraryID because
// the id names its own library, the same shape as SessionByID.
//
// Expiry is checked here as well as swept, and both are needed. The sweep is
// traffic-driven, so a session can be expired for a while before anything
// removes it; this check is what makes that interval safe.
func (s *Store) StewardSessionByID(ctx context.Context, id string, now time.Time) (boulevard.StewardSession, error) {
	var sess boulevard.StewardSession
	var libID, created, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, library_id, created_at, expires_at FROM steward_sessions WHERE id = ?`,
		id).Scan(&sess.ID, &libID, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.StewardSession{}, fmt.Errorf("steward session: %w", ErrNotFound)
	}
	if err != nil {
		return boulevard.StewardSession{}, fmt.Errorf("look up steward session: %w", err)
	}
	sess.LibraryID = boulevard.LibraryID(libID)
	if sess.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return boulevard.StewardSession{}, fmt.Errorf("steward session created_at: %w", err)
	}
	if sess.ExpiresAt, err = time.Parse(time.RFC3339, expires); err != nil {
		return boulevard.StewardSession{}, fmt.Errorf("steward session expires_at: %w", err)
	}
	if !now.Before(sess.ExpiresAt) {
		return boulevard.StewardSession{}, fmt.Errorf("steward session: %w", ErrNotFound)
	}
	return sess, nil
}

func (s *Store) RenewStewardSession(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE steward_sessions SET expires_at = ? WHERE id = ?`,
		expiresAt.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("renew steward session: %w", err)
	}
	return nil
}

func (s *Store) DeleteStewardSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM steward_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete steward session: %w", err)
	}
	return nil
}

// SweepExpiredStewardSessions is host-scoped and takes no LibraryID, for the
// reason SweepExpiredSessions takes none: the question is about the host.
func (s *Store) SweepExpiredStewardSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM steward_sessions WHERE expires_at < ?`,
		now.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("sweep expired steward sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep expired steward sessions: %w", err)
	}
	return n, nil
}
