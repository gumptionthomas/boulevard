package store

import (
	"context"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// seedLibrary builds a library and inserts it. Every test below needs a
// library row to exist — makeLibrary alone returns a value that was never
// written, and a Library that never came back from the database carries
// zeroes in its settings columns, which is the trap ApproveItem's comment
// already warns about.
func seedLibrary(t *testing.T, st *Store) boulevard.Library {
	t.Helper()
	lib := makeLibrary(t)
	if err := st.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatalf("create library: %v", err)
	}
	return lib
}

func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

func shelvedTestItem(t *testing.T, st *Store, id boulevard.LibraryID, itemID string, now time.Time) boulevard.Item {
	t.Helper()
	return shelvedTestItemAt(t, st, id, itemID, now)
}

func shelvedTestItemAt(t *testing.T, st *Store, id boulevard.LibraryID, itemID string, shelvedAt time.Time) boulevard.Item {
	t.Helper()
	at := shelvedAt
	it := boulevard.Item{
		ID: itemID, LibraryID: id, Type: boulevard.ItemLink,
		Payload: "https://example.com/" + itemID, Note: "note for " + itemID,
		CopiesTotal: 3, CopiesLeft: 3, State: boulevard.ItemShelved,
		LeftAt: shelvedAt, ShelvedAt: &at,
	}
	if err := st.CreateItem(context.Background(), id, it); err != nil {
		t.Fatalf("create item %s: %v", itemID, err)
	}
	return it
}

func TestSweepExpiredSessionsDeletesAndCascades(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	// seededLibrary, not seedLibrary: sessions.token_id is a real foreign key
	// (schema.sql) enforced by the _pragma=foreign_keys(1) DSN option this
	// very test relies on for its cascade assertion below. A fabricated
	// TokenID with no matching tokens row fails that same constraint before
	// the session insert can even happen — see the task report for detail.
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	live := boulevard.Session{
		ID: "LIVE", LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	dead := boulevard.Session{
		ID: "DEAD", LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	for _, sess := range []boulevard.Session{live, dead} {
		if err := st.CreateSession(context.Background(), sess); err != nil {
			t.Fatalf("create session %s: %v", sess.ID, err)
		}
	}

	it := shelvedTestItem(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`INSERT INTO session_takes (session_id, item_id, taken_at) VALUES (?, ?, ?)`,
		"DEAD", it.ID, now.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert take: %v", err)
	}

	n, err := st.SweepExpiredSessions(context.Background(), now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d sessions, want 1", n)
	}

	// The cascade is the point: this asserts foreign_keys(1) is on as much
	// as it asserts the schema. SQLite ignores ON DELETE CASCADE silently
	// without the pragma.
	var takes int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM session_takes WHERE session_id = 'DEAD'`).Scan(&takes); err != nil {
		t.Fatalf("count takes: %v", err)
	}
	if takes != 0 {
		t.Errorf("take rows left after sweep = %d, want 0 — is foreign_keys(1) set?", takes)
	}

	var live_ int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE id = 'LIVE'`).Scan(&live_); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live_ != 1 {
		t.Errorf("live session was swept")
	}
}

func TestSweepExpiredItemsShedsOnlyPastTheBoundary(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st) // max_age_days defaults to 90
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	// Shelved exactly 90 days ago: at the boundary, not past it. Stays.
	atBoundary := shelvedTestItemAt(t, st, lib.ID, "EDGE", now.AddDate(0, 0, -90))
	// A minute older. Goes.
	pastBoundary := shelvedTestItemAt(t, st, lib.ID, "OLD",
		now.AddDate(0, 0, -90).Add(-time.Minute))
	fresh := shelvedTestItemAt(t, st, lib.ID, "FRESH", now.Add(-time.Hour))

	n, err := st.SweepExpiredItems(context.Background(), lib.ID, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d items, want 1", n)
	}

	for _, tc := range []struct {
		id    string
		state boulevard.ItemState
	}{
		{atBoundary.ID, boulevard.ItemShelved},
		{pastBoundary.ID, boulevard.ItemShed},
		{fresh.ID, boulevard.ItemShelved},
	} {
		got, err := st.ItemByID(context.Background(), lib.ID, tc.id)
		if err != nil {
			t.Fatalf("read %s: %v", tc.id, err)
		}
		if got.State != tc.state {
			t.Errorf("item %s state = %s, want %s", tc.id, got.State, tc.state)
		}
	}

	shed, err := st.ItemByID(context.Background(), lib.ID, pastBoundary.ID)
	if err != nil {
		t.Fatalf("read shed item: %v", err)
	}
	if shed.ShedReason != boulevard.ShedExpired {
		t.Errorf("shed reason = %q, want %q", shed.ShedReason, boulevard.ShedExpired)
	}
	if shed.ShedAt == nil {
		t.Error("shed_at was not set")
	}
}

// TestSweepExpiredItemsNeverExpiresAtMaxAgeZero is F9: SweepExpiredItems
// guards max_age_days <= 0 as "never expire", not "shed everything". Getting
// that backwards sheds an entire shelf, and nothing exercised this branch
// before now.
func TestSweepExpiredItemsNeverExpiresAtMaxAgeZero(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	if _, err := st.db.Exec(`UPDATE libraries SET max_age_days = 0 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set max_age_days: %v", err)
	}
	old := shelvedTestItemAt(t, st, lib.ID, "OLD", now.AddDate(-1, 0, 0))

	n, err := st.SweepExpiredItems(context.Background(), lib.ID, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Fatalf("swept %d items, want 0 — max_age_days = 0 means never expire, not shed everything", n)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, old.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved — a year-old item was shed at max_age_days = 0", got.State)
	}
}

func TestSweepExpiredItemsSkipsPinned(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	old := shelvedTestItemAt(t, st, lib.ID, "PINNED", now.AddDate(0, 0, -400))
	if _, err := st.db.Exec(`UPDATE items SET pinned = 1 WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("pin: %v", err)
	}

	n, err := st.SweepExpiredItems(context.Background(), lib.ID, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("swept %d pinned items, want 0 — pins never expire (§5)", n)
	}
}
