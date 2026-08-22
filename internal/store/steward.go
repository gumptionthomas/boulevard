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
// any previous one, and revokes every live steward session on that library
// in the same transaction.
//
// Replacing the key is the whole reset story (DESIGN.md §6): a lost key, a
// key read aloud, one typed on a borrowed phone, a notebook photographed —
// and, the case this branch's own startup warning names, a `bl_steward`
// cookie captured off a plain-http connection. None of those are actually
// remedied if a session minted under the old key keeps working after the
// reset: the attacker's credential was never the key itself. Deleting the
// sessions here, inside SetStewardKeyHash, rather than leaving it to
// runStewardKey to remember, means no future caller of this method can
// forget it — the same reasoning that keeps steward_key_hash off the
// Library struct so UpdateLibrary cannot blank it by accident.
//
// The delete is library-scoped: a session belonging to a different library
// must survive a reset that was never about it.
func (s *Store) SetStewardKeyHash(ctx context.Context, id boulevard.LibraryID, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set steward key for %q: %w", id, err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
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

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM steward_sessions WHERE library_id = ?`, string(id)); err != nil {
		return fmt.Errorf("revoke steward sessions for %q: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set steward key for %q: %w", id, err)
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

// RenewStewardSession extends a live session's expiry. The write is
// conditional on `expires_at > now`: an unconditional UPDATE would revive a
// session that already expired, and such rows exist between expiry and
// whatever eventually sweeps them, exactly as with a presence Session — the
// store should not depend on every caller remembering to load through
// StewardSessionByID first.
//
// Because of that guard, zero rows affected means either "no such id" or
// "that id names a session that has already expired" — both report
// ErrNotFound, and a caller that treats ErrNotFound as impossible because it
// just loaded the session would be wrong in the second case if enough time
// passed between the load and this call.
func (s *Store) RenewStewardSession(ctx context.Context, id string, now, expiresAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE steward_sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
		expiresAt.UTC().Format(time.RFC3339), id, now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("renew steward session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("renew steward session: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("steward session: %w", ErrNotFound)
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
