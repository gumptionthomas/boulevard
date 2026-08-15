package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// SweepExpiredSessions deletes every session whose expires_at has passed,
// host-wide.
//
// It takes no LibraryID because the question is about the host, the same
// reason LibrarySlugs takes none.
//
// Milestone 1 decided against sweeping: "a background sweeper over a table
// holding at most a handful of live rows is machinery with no purpose."
// That answered whether an expired session grants access — it does not,
// expiry is checked on read — but not what an undeleted row means. It means
// a permanent record, and anything keyed to a session inherits that
// permanence. The objection was to a *background* sweeper specifically;
// this runs on the read path beside SweepExpiredItems, so there is no
// goroutine, no timer and no shutdown path.
//
// The take rows go with the session, by ON DELETE CASCADE. That is what
// bounds a session-to-item link to twenty-four hours and keeps it from
// being the user record §1 forbids.
//
// Expiry is still checked on read in SessionByID. A session can be expired
// but not yet swept — traffic drives the sweep — and the read check is what
// makes that safe. Do not remove it now that this exists.
func (s *Store) SweepExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`,
		now.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("sweep expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep expired sessions: %w", err)
	}
	return n, nil
}

// SweepExpiredItems sheds every shelved, non-pinned item in one library
// whose shelved_at is older than that library's max_age_days (§5).
//
// max_age_days is read here rather than taken from a caller-supplied
// Library, for the reason ApproveItem reads slots inside its transaction: a
// Library value that never came back from the database carries zeroes, and
// a zero max age sheds the entire shelf.
//
// One statement, run before the shelf query. On almost every request it
// touches no rows. It is deliberately not throttled: a throttle means a
// shelf can show an item past its expiry for the length of the window, and
// adds per-process state that a restart resets — something to reason about
// again in v2's bounded cache of library handles.
func (s *Store) SweepExpiredItems(ctx context.Context, id boulevard.LibraryID, now time.Time) (int64, error) {
	var maxAgeDays int
	err := s.db.QueryRowContext(ctx,
		`SELECT max_age_days FROM libraries WHERE id = ?`, string(id)).Scan(&maxAgeDays)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("library %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("read max_age_days for %q: %w", id, err)
	}
	if maxAgeDays <= 0 {
		// A steward who sets this to zero means "never expire", not "shed
		// everything". Refusing to act is the only safe reading.
		return 0, nil
	}

	cutoff := now.AddDate(0, 0, -maxAgeDays)
	res, err := s.db.ExecContext(ctx,
		`UPDATE items
		    SET state = 'shed', shed_at = ?, shed_reason = ?
		  WHERE library_id = ? AND state = 'shelved' AND pinned = 0
		    AND shelved_at < ?`,
		now.UTC().Format(time.RFC3339), string(boulevard.ShedExpired),
		string(id), cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("sweep expired items for %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep expired items for %q: %w", id, err)
	}
	return n, nil
}
