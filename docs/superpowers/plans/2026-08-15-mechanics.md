# Milestone 3 — mechanics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build take, undo, session sweeping, item expiry, the shed as a steward surface, and per-session rate limits.

**Architecture:** Sessions become sweepable, which bounds a session→item link to twenty-four hours and is what makes an undoable take possible without creating a user record. Take and undo are each one SQLite transaction in `internal/store`; the web layer adds two POST routes and four control states; the shed gets a CLI shaped exactly like Milestone 2's approval queue.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (pure Go — cgo would break the single-binary install), stdlib `net/http` pattern routing, `html/template` behind `go:embed`.

**Spec:** `docs/superpowers/specs/2026-08-15-mechanics-design.md`. Read the relevant section before implementing.

## Global Constraints

- **No new dependencies.** The binary must stay a one-file download. Nothing outside the standard library plus the three modules already in `go.mod`.
- **Clocks are injected, never read in place.** Every store method that needs the time takes `now time.Time`. `web.New` already takes `now func() time.Time`. No `time.Now()` inside a handler or a store method.
- **The store round-trips timestamps through RFC3339 in UTC.** Write `t.UTC().Format(time.RFC3339)`; parse with `time.Parse(time.RFC3339, s)`. Anything formatting a time for display must convert into the display zone first.
- **Use a non-UTC clock in any test that formats or compares a time.** A test pinning UTC on both sides passes while the real thing is a day out. This has shipped once already.
- **No store method infers a current library.** Every method takes an explicit `boulevard.LibraryID`, except resolution boundaries (`LibraryBySlug`, `TokenBySecret`, `SessionByID`, `DeleteSession`) and host-scoped queries (`LibrarySlugs`, and the new `SweepExpiredSessions`).
- **Migration columns live only in the migration, never also in `schema.sql`.** A fresh database runs both and fails on "duplicate column name".
- **Nothing fetches a submitted URL.** No titles, no thumbnails, no embeds, no validation by request.
- **Nothing logs a scan URL's path or puts a token secret in an error message.**
- **Every stranger-supplied string printed to a terminal goes through `boulevard.Sanitize`.** It closes a terminal-injection attack; it is not decoration.
- **`go vet ./...` and `gofmt -l .` must both be clean before every commit.**
- **Run the whole suite.** `go test ./...` is fast. Also run `TZ=America/Chicago go test -count=1 ./...` before the final commit of each task.
- **Eviction is FIFO on the oldest non-pinned item.** Never popularity-aware. `views` drives nothing.
- **Per-session limits: 3 leaves, 3 takes** (DESIGN.md §4).
- **Never hold an open `*sql.Rows` while issuing another query.** `SetMaxOpenConns(1)` means one unclosed `Rows` owns the only connection and the next query blocks forever — a hang, not an error. Use `QueryRow` where one row is wanted (it closes itself), and `defer rows.Close()` immediately where `Query` is unavoidable. This bit Task 1's own test.
- **The `store` package's test helpers are `openTemp(t) *Store` and `makeLibrary(t) boulevard.Library`** (both in `store_test.go`). `makeLibrary` builds a value; it does not insert — call `s.CreateLibrary(ctx, lib)` after it. Task 2 adds one shared helper, `seedLibrary(t, st)`, that does both; every later task in this package uses it rather than writing its own.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/boulevard/item.go` | `ShedReason` type; `Item.ShedAt`, `Item.ShedReason` | modify |
| `internal/store/migrate.go` | migration 4 | modify |
| `internal/store/sweep.go` | `SweepExpiredSessions`, `SweepExpiredItems` | create |
| `internal/store/item.go` | column list, `scanItem`, `ApproveItem` eviction reason | modify |
| `internal/store/take.go` | `TakeItem`, `UntakeItem`, `TakesForSession`, `TakenBySession` | create |
| `internal/store/shed.go` | `ShedItems`, `ReshelveItem`, `ReleaseItem` | create |
| `internal/store/session.go` | `IncrementLeaves`, `LeavesForSession` | modify |
| `internal/web/take.go` | take and untake handlers | create |
| `internal/web/routes.go` | two POST routes | modify |
| `internal/web/shelf.go` | sweeps, take view model, rate-limit state | modify |
| `internal/web/item.go` | sweeps | modify |
| `internal/web/leave.go` | leave rate limit | modify |
| `internal/web/templates/shelf.html`, `item.html`, `leave.html` | the four control states | modify |
| `cmd/boulevard/shed.go` | `shed`, `reshelve`, `release` | create |
| `cmd/boulevard/main.go` | wire the three subcommands | modify |
| `DESIGN.md`, `CLAUDE.md`, `docs/mechanics-acceptance.md` | amendments and checklist | modify/create |

---

## Task 1: Domain types and migration 4

**Files:**
- Modify: `internal/boulevard/item.go`
- Modify: `internal/store/migrate.go`
- Modify: `internal/store/item.go` (column list and `scanItem`)
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `boulevard.ShedReason` with constants `ShedEvicted`, `ShedExpired`, `ShedTaken`; `Item.ShedAt *time.Time`; `Item.ShedReason ShedReason`; migration version 4 creating `session_takes` and adding `sessions.leaves_used`, `items.shed_at`, `items.shed_reason`.

- [ ] **Step 1: Add the `ShedReason` type**

In `internal/boulevard/item.go`, after the `ItemState` block:

```go
// ShedReason records how an item reached the shed. The shed now fills from
// three different mechanisms and they read very differently to a steward
// deciding whether to re-shelve: an expired item is stale, an item taken to
// zero is the opposite.
//
// This is a label on a human decision, never an input to anything
// automatic. Eviction stays FIFO and popularity-blind (§5), and `views`
// still drives nothing (§7). Do not build automatic re-shelving on top of
// this.
type ShedReason string

const (
	ShedNone    ShedReason = ""
	ShedEvicted ShedReason = "evicted" // pushed off a full shelf, FIFO
	ShedExpired ShedReason = "expired" // older than max_age_days
	ShedTaken   ShedReason = "taken"   // taken to zero copies
)

// Label is how the shed CLI names a reason.
func (r ShedReason) Label() string {
	switch r {
	case ShedEvicted:
		return "made room"
	case ShedExpired:
		return "expired"
	case ShedTaken:
		return "taken to zero"
	default:
		return "shed"
	}
}
```

- [ ] **Step 2: Add the two fields and correct the stale comment**

In the same file, the `Item` struct's doc comment currently says "Sessions are never swept". That is no longer true and the sentence is load-bearing — replace the whole comment and add the fields:

```go
// Item is one thing on the shelf.
//
// There is deliberately no session or author reference. A left item is
// public and permanent, so a link from it to a session would point at
// durable data from the other side and survive the session sweep — the user
// record §1 forbids. Leaves are counted with a bare counter on the session
// row, which counts without linking.
//
// A take is the other case and is linked, in `session_takes`: it is a
// private act against a shelf, and the row is deleted when the session is
// swept, so the link cannot outlive twenty-four hours.
type Item struct {
```

and inside the struct, after `State ItemState`:

```go
	ShedAt     *time.Time
	ShedReason ShedReason
```

- [ ] **Step 3: Write the failing migration test**

In `internal/store/migrate_test.go`:

```go
func TestMigration4AddsShedColumnsAndSessionTakes(t *testing.T) {
	st := openTemp(t)

	var version int
	if err := st.db.QueryRow(
		`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version < 4 {
		t.Fatalf("schema version = %d, want at least 4", version)
	}

	for _, q := range []string{
		`SELECT leaves_used FROM sessions LIMIT 1`,
		`SELECT shed_at, shed_reason FROM items LIMIT 1`,
		`SELECT session_id, item_id, taken_at FROM session_takes LIMIT 1`,
	} {
		// Close every Rows before the next iteration. SetMaxOpenConns(1)
		// means an unclosed Rows owns the only connection and the next
		// query blocks forever — a hang, not a failure.
		rows, err := st.db.Query(q)
		if err != nil {
			t.Errorf("%s: %v", q, err)
			continue
		}
		rows.Close()
	}
}
```

- [ ] **Step 4: Run it and watch it fail**

Run: `go test -run TestMigration4 ./internal/store`
Expected: FAIL — `no such column: leaves_used`.

- [ ] **Step 5: Add migration 4**

In `internal/store/migrate.go`, after `createItems`:

```go
// addTakesAndShedReasons is Milestone 3.
//
// session_takes is what makes an undoable take possible without a user
// record. ON DELETE CASCADE is load-bearing: the session sweep deletes the
// session row and the take rows go with it, which is what bounds the link
// to twenty-four hours. It fires only because the DSN sets
// _pragma=foreign_keys(1) — SQLite ignores the clause silently otherwise,
// so a test asserts the cascade rather than the schema.
//
// The composite primary key gives take lookups their index and makes a
// duplicate take a constraint violation rather than a second row.
//
// No library_id on session_takes: §10 makes it first-class on every item,
// token, session and setting, and a join table is none of those. Every
// query reaches it through session_id, which is already library-scoped, and
// under v2's one-database-per-library both parents live in the same file.
const addTakesAndShedReasons = `
ALTER TABLE sessions ADD COLUMN leaves_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items    ADD COLUMN shed_at     TEXT;
ALTER TABLE items    ADD COLUMN shed_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS session_takes (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL REFERENCES items(id),
    taken_at   TEXT NOT NULL,
    PRIMARY KEY (session_id, item_id)
);
`
```

and add it to the slice:

```go
var migrations = []migration{
	{1, schema},
	{2, addLibrarySettings},
	{3, createItems},
	{4, addTakesAndShedReasons},
}
```

**Do not add any of these columns to `schema.sql`.** A fresh database runs both and fails on "duplicate column name". This is written on the `migration` type and has already caused one defect.

- [ ] **Step 6: Run it and watch it pass**

Run: `go test -run TestMigration4 ./internal/store`
Expected: PASS.

- [ ] **Step 7: Carry the new columns through the item queries**

In `internal/store/item.go`, extend the column list:

```go
const itemColumns = `id, library_id, type, payload, note, attribution,
                     copies_total, copies_left, state, pinned, views, takes,
                     left_at, shelved_at, shed_at, shed_reason`
```

`CreateItem` inserts by this list, so add two placeholders to its `VALUES` — the list is now sixteen columns plus `created_at`, so seventeen `?` — and two arguments after `shelved`:

```go
	var shedAt any
	if it.ShedAt != nil {
		shedAt = it.ShedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO items (`+itemColumns+`, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, string(id), string(it.Type), it.Payload, it.Note, it.Attribution,
		it.CopiesTotal, it.CopiesLeft, string(it.State), boolToInt(it.Pinned),
		it.Views, it.Takes,
		it.LeftAt.UTC().Format(time.RFC3339), shelved,
		shedAt, string(it.ShedReason),
		time.Now().UTC().Format(time.RFC3339))
```

- [ ] **Step 8: Extend `scanItem` to match**

In the same file, add the two scan targets and parse them:

```go
	var (
		it      boulevard.Item
		libID   string
		typ     string
		state   string
		pinned  int
		left    string
		shelved *string
		shedAt  *string
		reason  string
	)
	if err := sc.Scan(&it.ID, &libID, &typ, &it.Payload, &it.Note, &it.Attribution,
		&it.CopiesTotal, &it.CopiesLeft, &state, &pinned, &it.Views, &it.Takes,
		&left, &shelved, &shedAt, &reason); err != nil {
		return boulevard.Item{}, err
	}
```

and after the `shelved` block:

```go
	if shedAt != nil {
		at, err := time.Parse(time.RFC3339, *shedAt)
		if err != nil {
			return boulevard.Item{}, fmt.Errorf("item %s shed_at: %w", it.ID, err)
		}
		it.ShedAt = &at
	}
	it.ShedReason = boulevard.ShedReason(reason)
```

- [ ] **Step 9: Run the whole suite**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: every package PASS, vet silent, gofmt lists nothing.

- [ ] **Step 10: Commit**

```bash
git add internal/boulevard/item.go internal/store/migrate.go internal/store/item.go internal/store/migrate_test.go
git commit -m "feat: shed reasons, take rows, and the leaves counter"
```

---

## Task 2: The two sweeps

**Files:**
- Create: `internal/store/sweep.go`
- Test: `internal/store/sweep_test.go`

**Interfaces:**
- Consumes: migration 4 from Task 1.
- Produces:
  - `func (s *Store) SweepExpiredSessions(ctx context.Context, now time.Time) (int64, error)`
  - `func (s *Store) SweepExpiredItems(ctx context.Context, id boulevard.LibraryID, now time.Time) (int64, error)`

- [ ] **Step 1: Write the failing tests**

In `internal/store/sweep_test.go`. Note the clock: `America/Chicago`, never UTC.

First, the two helpers every later task in this package reuses. `store_test.go` already has `openTemp(t) *Store` and `makeLibrary(t) boulevard.Library`; `makeLibrary` only builds the value, so nothing yet does both.

```go
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

func TestSweepExpiredSessionsDeletesAndCascades(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	live := boulevard.Session{
		ID: "LIVE", LibraryID: lib.ID, TokenID: "T1",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	dead := boulevard.Session{
		ID: "DEAD", LibraryID: lib.ID, TokenID: "T1",
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
```

Add these helpers to `internal/store/sweep_test.go` if the package does not already have equivalents — check `item_test.go` first and reuse rather than duplicate:

```go
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
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run TestSweep ./internal/store`
Expected: FAIL — `st.SweepExpiredSessions undefined`.

- [ ] **Step 3: Write `internal/store/sweep.go`**

```go
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
```

- [ ] **Step 4: Run them and watch them pass**

Run: `go test -run TestSweep ./internal/store`
Expected: PASS, all three.

- [ ] **Step 5: Run the whole suite under a non-UTC zone**

Run: `TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: every package PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/sweep.go internal/store/sweep_test.go
git commit -m "feat: sweep expired sessions and shed expired items"
```

---

## Task 3: Eviction records its reason

**Files:**
- Modify: `internal/store/item.go` (`ApproveItem`)
- Test: `internal/store/item_test.go`

**Interfaces:**
- Consumes: `boulevard.ShedEvicted` from Task 1.
- Produces: nothing new; `ApproveItem` keeps its signature `(ctx, id, itemID string, now time.Time) (string, error)`.

- [ ] **Step 1: Write the failing test**

In `internal/store/item_test.go`:

```go
func TestApproveItemRecordsWhyItEvicted(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}

	first := shelvedTestItemAt(t, st, lib.ID, "FIRST", now.Add(-time.Hour))
	second := pendingTestItem(t, st, lib.ID, "SECOND", now)

	evicted, err := st.ApproveItem(context.Background(), lib.ID, second.ID, now)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if evicted != first.ID {
		t.Fatalf("evicted %q, want %q", evicted, first.ID)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, first.ID)
	if err != nil {
		t.Fatalf("read evicted item: %v", err)
	}
	if got.ShedReason != boulevard.ShedEvicted {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedEvicted)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set on the evicted item")
	}
}
```

Reuse the package's existing helper for a pending item if one exists; otherwise add:

```go
func pendingTestItem(t *testing.T, st *Store, id boulevard.LibraryID, itemID string, now time.Time) boulevard.Item {
	t.Helper()
	it := boulevard.Item{
		ID: itemID, LibraryID: id, Type: boulevard.ItemLink,
		Payload: "https://example.com/" + itemID, Note: "note for " + itemID,
		State: boulevard.ItemPending, LeftAt: now,
	}
	if err := st.CreateItem(context.Background(), id, it); err != nil {
		t.Fatalf("create pending item %s: %v", itemID, err)
	}
	return it
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test -run TestApproveItemRecordsWhy ./internal/store`
Expected: FAIL — shed reason is `""`.

- [ ] **Step 3: Set the reason on eviction**

In `ApproveItem`, the eviction `UPDATE` currently sets only `state`. Replace it:

```go
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = ?
			  WHERE library_id = ? AND id = ?`,
			now.UTC().Format(time.RFC3339), string(boulevard.ShedEvicted),
			string(id), evicted); err != nil {
			return "", fmt.Errorf("shed item %q: %w", evicted, err)
		}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `go test -run TestApproveItem ./internal/store`
Expected: PASS, including the existing approval tests.

- [ ] **Step 5: Run the whole suite and commit**

```bash
go test ./... && go vet ./... && gofmt -l .
git add internal/store/item.go internal/store/item_test.go
git commit -m "feat: an evicted item records that it was evicted"
```

---

## Task 4: Take and undo in the store

This is the core of the milestone and the fiddliest code in it. Read spec §3 and §4 before starting.

**Files:**
- Create: `internal/store/take.go`
- Test: `internal/store/take_test.go`

**Interfaces:**
- Consumes: `session_takes` from Task 1; `boulevard.ShedTaken` from Task 1.
- Produces:
  - `func (s *Store) TakeItem(ctx context.Context, id boulevard.LibraryID, sessionID boulevard.SessionID, itemID string, now time.Time, maxTakes int) error`
  - `func (s *Store) UntakeItem(ctx context.Context, id boulevard.LibraryID, sessionID boulevard.SessionID, itemID string) error`
  - `func (s *Store) TakenBySession(ctx context.Context, sessionID boulevard.SessionID) (map[string]bool, error)`
  - `var ErrLimitReached, ErrNoCopiesLeft, ErrShelfFull error`

- [ ] **Step 1: Add the three sentinel errors**

In `internal/store/store.go`, beside the existing `ErrNotFound` / `ErrNotPending`:

```go
	// ErrLimitReached is the per-session rate limit (DESIGN.md §4: 3 leaves,
	// 3 takes). Presence is attestable, not enforceable — scanning again
	// mints a new session with fresh counters, and that is not a loophole to
	// close.
	ErrLimitReached = errors.New("session limit reached")

	// ErrNoCopiesLeft guards a shelved item with no copies. An item that
	// reaches zero sheds in the same transaction, so this should be
	// unreachable — except for a steward who sets default_copies = 0, which
	// shelves items with nothing to take. Not dead code.
	ErrNoCopiesLeft = errors.New("no copies left")

	// ErrShelfFull is undo and re-shelve meeting a full shelf. Neither
	// evicts to make room: a stray tap must not cost a different item its
	// place, and eviction is FIFO precisely so nobody's behaviour reorders
	// the shelf.
	ErrShelfFull = errors.New("shelf is full")
```

- [ ] **Step 2: Write the failing tests**

In `internal/store/take_test.go`. These are the milestone's most important tests — every one covers a case the spec calls out explicitly.

```go
func takeSetup(t *testing.T) (*Store, boulevard.Library, boulevard.SessionID, time.Time) {
	t.Helper()
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	sess := boulevard.Session{
		ID: "SESSION1", LibraryID: lib.ID, TokenID: "T1",
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return st, lib, sess.ID, now
}

func TestTakeItemDecrementsAndCounts(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 {
		t.Errorf("copies_left = %d, want 2", got.CopiesLeft)
	}
	if got.Takes != 1 {
		t.Errorf("takes = %d, want 1", got.Takes)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
}

func TestTakeToZeroSheds(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Fatalf("state = %s, want shed", got.State)
	}
	if got.ShedReason != boulevard.ShedTaken {
		t.Errorf("shed reason = %q, want %q", got.ShedReason, boulevard.ShedTaken)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set")
	}
}

func TestDuplicateTakeIsANoOpNotAnError(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)

	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("first take: %v", err)
	}
	// Phones retry. A duplicate must not spend a second copy or fail.
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("second take: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 2 {
		t.Errorf("copies_left = %d, want 2 — the retry spent a copy", got.CopiesLeft)
	}
	if got.Takes != 1 {
		t.Errorf("takes = %d, want 1", got.Takes)
	}
}

func TestTakeRefusesPastTheLimit(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	for i, id := range []string{"I1", "I2", "I3"} {
		it := shelvedTestItemAt(t, st, lib.ID, id, now.Add(time.Duration(i)*time.Minute))
		if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
			t.Fatalf("take %s: %v", id, err)
		}
	}
	fourth := shelvedTestItemAt(t, st, lib.ID, "I4", now)

	err := st.TakeItem(context.Background(), lib.ID, sess, fourth.ID, now, 3)
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("fourth take error = %v, want ErrLimitReached", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, fourth.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — a refused take spent a copy", got.CopiesLeft)
	}
}

func TestTakeRefusesPinnedItems(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "PINNED", now)
	if _, err := st.db.Exec(`UPDATE items SET pinned = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("pin: %v", err)
	}

	err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("take of a pinned item = %v, want ErrNotFound — pins cannot be taken (§5)", err)
	}
}

func TestTakeRefusesWhenNoCopiesLeft(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "EMPTY", now)
	// The default_copies = 0 case: shelved with nothing to take.
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 0, copies_total = 0 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("empty it: %v", err)
	}

	err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3)
	if !errors.Is(err, ErrNoCopiesLeft) {
		t.Fatalf("error = %v, want ErrNoCopiesLeft", err)
	}
}

func TestUntakeRestoresAndUnsheds(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}

	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
	if got.CopiesLeft != 1 {
		t.Errorf("copies_left = %d, want 1", got.CopiesLeft)
	}
	if got.Takes != 0 {
		t.Errorf("takes = %d, want 0", got.Takes)
	}
	if got.ShedAt != nil || got.ShedReason != boulevard.ShedNone {
		t.Errorf("shed_at/%v shed_reason/%q were not cleared", got.ShedAt, got.ShedReason)
	}

	taken, err := st.TakenBySession(context.Background(), sess)
	if err != nil {
		t.Fatalf("taken: %v", err)
	}
	if taken[it.ID] {
		t.Error("the take row survived the undo")
	}
}

func TestUntakeDoesNotUnshedAnItemShedForAnotherReason(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// It expires while the take is outstanding. The steward decides whether
	// that comes back, not a passer-by.
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — undo un-shed an expired item", got.State)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — the copy should still return", got.CopiesLeft)
	}
}

func TestUntakeRefusesAFullShelf(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	it := shelvedTestItemAt(t, st, lib.ID, "TAKEN", now)
	if _, err := st.db.Exec(
		`UPDATE items SET copies_left = 1, copies_total = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatalf("set copies: %v", err)
	}
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	// Someone else's item fills the slot the take freed.
	shelvedTestItemAt(t, st, lib.ID, "NEWER", now.Add(time.Minute))

	err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID)
	if !errors.Is(err, ErrShelfFull) {
		t.Fatalf("untake error = %v, want ErrShelfFull", err)
	}

	newer, err := st.ItemByID(context.Background(), lib.ID, "NEWER")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if newer.State != boulevard.ItemShelved {
		t.Error("undo evicted a different item to make room")
	}
}

func TestDuplicateUntakeIsANoOp(t *testing.T) {
	st, lib, sess, now := takeSetup(t)
	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.TakeItem(context.Background(), lib.ID, sess, it.ID, now, 3); err != nil {
		t.Fatalf("take: %v", err)
	}
	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("first untake: %v", err)
	}
	if err := st.UntakeItem(context.Background(), lib.ID, sess, it.ID); err != nil {
		t.Fatalf("second untake: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CopiesLeft != 3 {
		t.Errorf("copies_left = %d, want 3 — the retry restored a phantom copy", got.CopiesLeft)
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `go test -run 'TestTake|TestUntake|TestDuplicate' ./internal/store`
Expected: FAIL — `st.TakeItem undefined`.

- [ ] **Step 4: Write `internal/store/take.go`**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// TakeItem records that a session took one copy of an item (DESIGN.md §5).
//
// Taking is a write. It decrements a finite shelf, so it is gated exactly
// like leaving — take feels passive because you are receiving, but on a
// finite shelf removal is as much an edit as addition.
//
// Everything happens in one transaction: the take row, the decrement, and
// the shed at zero. Two of the three without the others would leave the
// shelf describing something that did not happen.
//
// A duplicate take by the same session on the same item is a no-op that
// returns nil. Phones retry, and a retry must not spend a second copy or
// produce a failure page.
func (s *Store) TakeItem(ctx context.Context, id boulevard.LibraryID,
	sessionID boulevard.SessionID, itemID string, now time.Time, maxTakes int) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin take: %w", err)
	}
	defer tx.Rollback()

	// Already taken by this session? Nothing to do.
	var already int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_takes WHERE session_id = ? AND item_id = ?`,
		string(sessionID), itemID).Scan(&already); err != nil {
		return fmt.Errorf("check existing take: %w", err)
	}
	if already > 0 {
		return nil
	}

	var takes int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_takes WHERE session_id = ?`,
		string(sessionID)).Scan(&takes); err != nil {
		return fmt.Errorf("count takes: %w", err)
	}
	if takes >= maxTakes {
		return fmt.Errorf("session has taken %d: %w", takes, ErrLimitReached)
	}

	var state string
	var copiesLeft, pinned int
	err = tx.QueryRowContext(ctx,
		`SELECT state, copies_left, pinned FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state, &copiesLeft, &pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	// A pinned item has no take control to press, so a take against one is
	// not a refusal to explain — it is a URL that names nothing takeable.
	if boulevard.ItemState(state) != boulevard.ItemShelved || pinned == 1 {
		return fmt.Errorf("item %q is not takeable: %w", itemID, ErrNotFound)
	}
	if copiesLeft <= 0 {
		return fmt.Errorf("item %q: %w", itemID, ErrNoCopiesLeft)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_takes (session_id, item_id, taken_at) VALUES (?, ?, ?)`,
		string(sessionID), itemID, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record take: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET copies_left = copies_left - 1, takes = takes + 1
		  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
		return fmt.Errorf("decrement copies on %q: %w", itemID, err)
	}

	if copiesLeft-1 == 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = ?
			  WHERE library_id = ? AND id = ?`,
			now.UTC().Format(time.RFC3339), string(boulevard.ShedTaken),
			string(id), itemID); err != nil {
			return fmt.Errorf("shed item %q at zero: %w", itemID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit take: %w", err)
	}
	return nil
}

// UntakeItem reverses a take while the session that made it still exists
// (spec §4). This is the whole reason sessions became sweepable: the take
// row is deleted with the session, so the undo window closes on its own.
//
// It checks shed_reason, not just state. An item shed by expiry or eviction
// while the take was outstanding must not be silently re-shelved by a
// passer-by's undo — that is the steward's call. The same guard covers an
// item the steward released out of the shed: `released` is not `shed`, so
// the un-shed does not fire, and a steward's release cannot be reversed by
// a stranger.
//
// A duplicate undo is a no-op that returns nil, for the retry reason
// TakeItem documents.
func (s *Store) UntakeItem(ctx context.Context, id boulevard.LibraryID,
	sessionID boulevard.SessionID, itemID string) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin untake: %w", err)
	}
	defer tx.Rollback()

	var state, reason string
	err = tx.QueryRowContext(ctx,
		`SELECT state, shed_reason FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}

	unshed := boulevard.ItemState(state) == boulevard.ItemShed &&
		boulevard.ShedReason(reason) == boulevard.ShedTaken

	// Capacity is checked before anything is written, and only when the
	// undo would actually put an item back. Refusing is deliberate: making
	// room would evict someone else's item to fix this person's stray tap.
	if unshed {
		var slots, shelved int
		if err := tx.QueryRowContext(ctx,
			`SELECT slots FROM libraries WHERE id = ?`, string(id)).Scan(&slots); err != nil {
			return fmt.Errorf("read slots for %q: %w", id, err)
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved'`,
			string(id)).Scan(&shelved); err != nil {
			return fmt.Errorf("count the shelf: %w", err)
		}
		if shelved >= slots {
			return fmt.Errorf("shelf has %d of %d slots: %w", shelved, slots, ErrShelfFull)
		}
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM session_takes WHERE session_id = ? AND item_id = ?`,
		string(sessionID), itemID)
	if err != nil {
		return fmt.Errorf("delete take: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete take: %w", err)
	}
	if n == 0 {
		return nil // nothing was taken; nothing to undo
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET copies_left = copies_left + 1, takes = takes - 1
		  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
		return fmt.Errorf("restore copy on %q: %w", itemID, err)
	}

	if unshed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET state = 'shelved', shed_at = NULL, shed_reason = ''
			  WHERE library_id = ? AND id = ?`, string(id), itemID); err != nil {
			return fmt.Errorf("re-shelve %q: %w", itemID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit untake: %w", err)
	}
	return nil
}

// TakenBySession is the set of item ids this session has taken, for
// rendering the take control's "Taken / Put it back" state.
//
// A set rather than a count: the shelf needs to know which items, and the
// rate limit needs how many, and len() answers the second from the first.
// One query, no second number to keep in sync.
func (s *Store) TakenBySession(ctx context.Context, sessionID boulevard.SessionID) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id FROM session_takes WHERE session_id = ?`, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list takes for session: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan take: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run them and watch them pass**

Run: `go test -run 'TestTake|TestUntake|TestDuplicate' ./internal/store`
Expected: PASS, all ten.

- [ ] **Step 6: Run the whole suite under a non-UTC zone and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/store/take.go internal/store/take_test.go internal/store/store.go
git commit -m "feat: take, undo, and the rules that keep both honest"
```

---

## Task 5: The shed in the store

**Files:**
- Create: `internal/store/shed.go`
- Test: `internal/store/shed_test.go`

**Interfaces:**
- Consumes: `ErrShelfFull`, `ErrNotFound` from Task 4.
- Produces:
  - `func (s *Store) ShedItems(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Item, error)`
  - `func (s *Store) ReshelveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) error`
  - `func (s *Store) ReleaseItem(ctx context.Context, id boulevard.LibraryID, itemID string) error`

- [ ] **Step 1: Write the failing tests**

In `internal/store/shed_test.go`:

```go
func TestShedItemsNewestFirst(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	older := shelvedTestItemAt(t, st, lib.ID, "OLDER", now.Add(-2*time.Hour))
	newer := shelvedTestItemAt(t, st, lib.ID, "NEWER", now.Add(-time.Hour))
	shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)

	for i, it := range []boulevard.Item{older, newer} {
		at := now.Add(time.Duration(i) * time.Hour)
		if _, err := st.db.Exec(
			`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = 'expired'
			  WHERE id = ?`, at.UTC().Format(time.RFC3339), it.ID); err != nil {
			t.Fatalf("shed %s: %v", it.ID, err)
		}
	}

	got, err := st.ShedItems(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("shed items: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d shed items, want 2", len(got))
	}
	if got[0].ID != newer.ID {
		t.Errorf("first shed item = %s, want %s (newest first)", got[0].ID, newer.ID)
	}
}

func TestReshelveRestoresCopiesAndRestartsTheClock(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now.AddDate(0, 0, -200))
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = 'taken',
		        copies_left = 0 WHERE id = ?`,
		now.UTC().Format(time.RFC3339), it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now); err != nil {
		t.Fatalf("reshelve: %v", err)
	}

	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("state = %s, want shelved", got.State)
	}
	if got.CopiesLeft != got.CopiesTotal {
		t.Errorf("copies_left = %d, want copies_total %d", got.CopiesLeft, got.CopiesTotal)
	}
	if got.ShedAt != nil || got.ShedReason != boulevard.ShedNone {
		t.Error("shed_at/shed_reason were not cleared")
	}
	// A re-shelved item is new to the shelf again, so its expiry clock
	// restarts. Otherwise it would shed again on the next sweep.
	if got.ShelvedAt == nil || !got.ShelvedAt.Equal(now.UTC().Truncate(time.Second)) {
		t.Errorf("shelved_at = %v, want %v", got.ShelvedAt, now.UTC())
	}
}

func TestReshelveRefusesAFullShelf(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)
	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now.Add(-time.Hour))
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now)
	if !errors.Is(err, ErrShelfFull) {
		t.Fatalf("reshelve error = %v, want ErrShelfFull", err)
	}

	on, err := st.ItemByID(context.Background(), lib.ID, "ONSHELF")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if on.State != boulevard.ItemShelved {
		t.Error("reshelve evicted a live item to make room")
	}
}

func TestReleaseFromTheShed(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)

	it := shelvedTestItemAt(t, st, lib.ID, "SHED1", now)
	if _, err := st.db.Exec(
		`UPDATE items SET state = 'shed', shed_reason = 'expired' WHERE id = ?`,
		it.ID); err != nil {
		t.Fatalf("shed: %v", err)
	}

	if err := st.ReleaseItem(context.Background(), lib.ID, it.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Soft delete: the row stays, so a steward who released the wrong thing
	// has not destroyed it.
	got, err := st.ItemByID(context.Background(), lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemReleased {
		t.Errorf("state = %s, want released", got.State)
	}
}

func TestReshelveAndReleaseRefuseItemsNotInTheShed(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	it := shelvedTestItemAt(t, st, lib.ID, "ONSHELF", now)

	if err := st.ReshelveItem(context.Background(), lib.ID, it.ID, now); err == nil {
		t.Error("reshelved an item that was already on the shelf")
	}
	if err := st.ReleaseItem(context.Background(), lib.ID, it.ID); err == nil {
		t.Error("released an item from the shelf rather than the shed")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run 'TestShed|TestReshelve|TestRelease' ./internal/store`
Expected: FAIL — `st.ShedItems undefined`.

- [ ] **Step 3: Write `internal/store/shed.go`**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// ShedItems is the steward's shed: most recently shed first, because the
// decision a steward is making is usually about what just left.
//
// Evicted items are not public and not deleted. The shelf sheds them —
// passively, the way a tree sheds leaves, with no judgment implied (§5).
func (s *Store) ShedItems(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Item, error) {
	return s.itemsWhere(ctx, id,
		`WHERE library_id = ? AND state = 'shed' ORDER BY shed_at DESC, id DESC`)
}

// ReshelveItem returns a shed item to the shelf.
//
// shelved_at is set to now, restarting the expiry clock: a re-shelved item
// is new to the shelf again, and keeping the old timestamp would shed it
// again on the very next sweep. Copies are restored to copies_total, since
// an item taken to zero would otherwise return with nothing to take.
//
// It refuses a full shelf rather than evicting, for the reason UntakeItem
// does: the steward asked to add one item, not to remove another. §5 also
// warns against mechanics that generate steward labor — silently evicting
// here would create exactly the surprise that produces more of it.
func (s *Store) ReshelveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reshelve: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemShed {
		return fmt.Errorf("item %q is %s, not in the shed: %w", itemID, state, ErrNotShed)
	}

	var slots, shelved int
	if err := tx.QueryRowContext(ctx,
		`SELECT slots FROM libraries WHERE id = ?`, string(id)).Scan(&slots); err != nil {
		return fmt.Errorf("read slots for %q: %w", id, err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved'`,
		string(id)).Scan(&shelved); err != nil {
		return fmt.Errorf("count the shelf: %w", err)
	}
	if shelved >= slots {
		return fmt.Errorf("shelf has %d of %d slots: %w", shelved, slots, ErrShelfFull)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items
		    SET state = 'shelved', shelved_at = ?, shed_at = NULL, shed_reason = '',
		        copies_left = copies_total
		  WHERE library_id = ? AND id = ?`,
		now.UTC().Format(time.RFC3339), string(id), itemID); err != nil {
		return fmt.Errorf("reshelve %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reshelve: %w", err)
	}
	return nil
}

// ReleaseItem soft-deletes a shed item. The row stays, so a steward who
// releases the wrong thing has not destroyed it — the same soft delete
// RejectItem applies to the approval queue.
func (s *Store) ReleaseItem(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin release: %w", err)
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemShed {
		return fmt.Errorf("item %q is %s, not in the shed: %w", itemID, state, ErrNotShed)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET state = 'released' WHERE library_id = ? AND id = ?`,
		string(id), itemID); err != nil {
		return fmt.Errorf("release %q: %w", itemID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit release: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Add the `ErrNotShed` sentinel**

In `internal/store/store.go`, beside the others:

```go
	// ErrNotShed distinguishes "no such item" from "that item is not in the
	// shed", the way ErrNotPending does for the approval queue. A steward
	// needs to tell a mistyped handle from a second `reshelve` on the same
	// item.
	ErrNotShed = errors.New("item is not in the shed")
```

- [ ] **Step 5: Run them and watch them pass**

Run: `go test -run 'TestShed|TestReshelve|TestRelease' ./internal/store`
Expected: PASS, all five.

- [ ] **Step 6: Run the whole suite and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/store/shed.go internal/store/shed_test.go internal/store/store.go
git commit -m "feat: the shed, re-shelving, and release"
```

---

## Task 6: Take and undo on the web

**Files:**
- Create: `internal/web/take.go`
- Modify: `internal/web/routes.go`
- Modify: `internal/web/shelf.go` (`pageData`, `itemView`)
- Modify: `internal/web/templates/shelf.html`, `internal/web/templates/item.html`
- Test: `internal/web/take_test.go`

**Interfaces:**
- Consumes: `TakeItem`, `UntakeItem`, `TakenBySession`, `ErrLimitReached`, `ErrNoCopiesLeft`, `ErrShelfFull` from Task 4.
- Produces: `POST /b/{slug}/i/{id}/take`, `POST /b/{slug}/i/{id}/untake`; an `itemView` type carrying per-item control state to the templates.

- [ ] **Step 1: Add the routes**

In `internal/web/routes.go`, after the existing leave routes:

```go
	mux.HandleFunc("POST /b/{slug}/i/{id}/take", s.handleTake)
	mux.HandleFunc("POST /b/{slug}/i/{id}/untake", s.handleUntake)
```

`POST` only, and deliberately. A `GET` take URL would be shareable, prefetchable by a browser or link scanner, and — because the response redirects to a submitted URL — an open redirect wearing the shelf's domain. Requiring `POST` removes all three. Do not add a `GET` form of these for convenience.

- [ ] **Step 2: Add the per-item view model**

In `internal/web/shelf.go`, beside `pageData`:

```go
// itemView is one item plus what this viewer may do with it. The template
// must not work that out itself: the same shelf URL renders four different
// controls depending on the cookie, and a branch spread across two
// templates is how they drift apart.
type itemView struct {
	boulevard.Item
	// TakenByYou is set when this session already took a copy, which turns
	// the control into "Taken / Put it back".
	TakenByYou bool
	// AtLimit is set when this session has taken its three. The control
	// goes inert with an explanation rather than disappearing — show the
	// rule, do not hide the feature (§6).
	AtLimit bool
}

// Takeable reports whether the control renders at all. A pinned item shows
// nothing: it has no copies and cannot be taken (§5), so there is no rule to
// explain — a pin is furniture, not stock.
func (v itemView) Takeable() bool { return !v.Pinned }
```

Add `ItemViews []itemView` to `pageData` and a `MaxTakes int` field for the copy.

- [ ] **Step 3: Write the failing handler tests**

In `internal/web/take_test.go`:

```go
func TestTakeRedirectsToThePayload(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://example.com/thing" {
		t.Errorf("Location = %q, want the payload", got)
	}
}

func TestTakeOfTextRedirectsToTheShelfAnchor(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemText, "some words")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	want := "/b/" + lib.Slug + "/#i-" + itemID
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q — a text item has nothing to open", got, want)
	}
}

func TestTakeWithoutASessionIsRefused(t *testing.T) {
	srv, lib, _, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Scan the code at the box") {
		t.Error("the 403 does not explain the rule")
	}
}

func TestTakeIsPostOnly(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	req := httptest.NewRequest(http.MethodGet,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusSeeOther {
		t.Fatal("GET take succeeded — that is a shareable, prefetchable open redirect")
	}
}

func TestUndoReturnsToTheShelf(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	take := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	take.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), take)

	undo := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/untake", nil)
	undo.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, undo)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := "/b/" + lib.Slug + "/#i-" + itemID
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestShelfShowsPutItBackAfterATake(t *testing.T) {
	srv, lib, cookie, itemID := takeServer(t, boulevard.ItemLink, "https://example.com/thing")

	take := httptest.NewRequest(http.MethodPost,
		"/b/"+lib.Slug+"/i/"+itemID+"/take", nil)
	take.AddCookie(cookie)
	srv.ServeHTTP(httptest.NewRecorder(), take)

	shelf := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	shelf.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, shelf)

	body := rec.Body.String()
	if !strings.Contains(body, "Put it back") {
		t.Error("the shelf does not offer the undo after a take")
	}
}
```

Write `takeServer` as a helper in the same file, following the pattern already used by `internal/web/leave_test.go` for building a server, library, session cookie and item. It returns `(*Server, boulevard.Library, *http.Cookie, string)` — server, library, a cookie for a live session, and the id of one shelved item of the given type and payload.

- [ ] **Step 4: Run them and watch them fail**

Run: `go test -run 'TestTake|TestUndo|TestShelfShows' ./internal/web`
Expected: FAIL — 404, the routes do not exist.

- [ ] **Step 5: Write `internal/web/take.go`**

```go
package web

import (
	"errors"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// maxTakes is DESIGN.md §4's per-session take limit.
//
// Presence is attestable, not enforceable: someone standing at the box may
// scan again for a fresh session with fresh counters. That is not a
// loophole to close. Rotation plus rate limits bound the blast radius, and
// §4 says explicitly not to attempt to make this airtight.
const maxTakes = 3

// handleTake records a take and sends the visitor to the thing.
//
// One tap does both. With no JavaScript that means a 303 to the item's
// payload, which is already displayed and clickable on the shelf — so this
// exposes nothing a reader could not already reach. Nothing fetches it (§3);
// it goes into the Location header as stored.
func (s *Server) handleTake(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	itemID := r.PathValue("id")

	sess, live := s.liveSession(r, lib.ID)
	if !live {
		s.refuseTake(w, r, lib, http.StatusForbidden, "")
		return
	}

	it, err := s.store.ItemByID(r.Context(), lib.ID, itemID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	err = s.store.TakeItem(r.Context(), lib.ID, sess.ID, itemID, s.now(), maxTakes)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrLimitReached):
		s.refuseTake(w, r, lib, http.StatusForbidden,
			"You've taken three things with this scan. Scan the card again for more.")
		return
	case errors.Is(err, store.ErrNoCopiesLeft):
		s.refuseTake(w, r, lib, http.StatusConflict,
			"Someone took the last one.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, takeDestination(lib, it), http.StatusSeeOther)
}

// takeDestination is where a successful take sends you. A text item has
// nothing to open, so it goes back to the shelf anchored at itself.
func takeDestination(lib boulevard.Library, it boulevard.Item) string {
	if it.Type == boulevard.ItemText {
		return shelfURL(lib) + "#i-" + it.ID
	}
	return it.Payload
}

// handleUntake reverses a take, and always returns to the shelf.
func (s *Server) handleUntake(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	itemID := r.PathValue("id")

	sess, live := s.liveSession(r, lib.ID)
	if !live {
		s.refuseTake(w, r, lib, http.StatusForbidden, "")
		return
	}

	err := s.store.UntakeItem(r.Context(), lib.ID, sess.ID, itemID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrShelfFull):
		s.refuseTake(w, r, lib, http.StatusConflict,
			"The shelf filled up while this was gone. It's in the shed — the steward can put it back.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, shelfURL(lib)+"#i-"+itemID, http.StatusSeeOther)
}

// refuseTake re-renders the shelf with a message, at the given status.
//
// Always the shelf, whichever page the control was pressed on. Carrying the
// origin page through the request would buy a slightly better return for a
// case that should not happen.
func (s *Server) refuseTake(w http.ResponseWriter, r *http.Request,
	lib boulevard.Library, status int, msg string) {
	s.renderShelf(w, r, lib, status, msg)
}
```

Milestone 2 centralised a library's URLs in `internal/web/routes.go` — `shelfPath(slug string)`, `shelfURL(lib)`, `leaveURL(lib)`, `leftURL(lib)`. Use `shelfURL`; do not build paths by hand. There is no item-URL helper yet, so add one beside the others and use it everywhere, including the templates:

```go
func itemURL(lib boulevard.Library, id string) string { return shelfURL(lib) + "i/" + id }
```

The templates need a base rather than a per-item call, so add `ItemBase string` to `pageData` and set it to `shelfURL(lib) + "i/"` wherever `pageData` is built for the shelf and item pages. That is what `{{$.ItemBase}}` refers to in step 7.

- [ ] **Step 6: Give the shelf handler its message and item views**

Refactor `handleShelf` so its body is `s.renderShelf(w, r, lib, http.StatusOK, "")`, and `renderShelf` does the work: loads items, loads `TakenBySession` when there is a session, builds `[]itemView`, and sets `Notice` on `pageData` from `msg`. This is the one place that decides a control's state, so both templates read the same answer.

- [ ] **Step 7: Render the four states**

In `shelf.html`, replace the inert take line. Each item's wrapper gains `id="i-{{.ID}}"` so the anchors above land:

```html
<div class="item" id="i-{{.ID}}">
  <p class="note"><a href="{{$.ItemBase}}{{.ID}}">{{.Note}}</a></p>
  ...
  {{if .Takeable}}
    {{if not $.HasSession}}
      <p class="dim"><span class="inert">Take</span> {{.CopiesLeft}} copies</p>
    {{else if .TakenByYou}}
      <form method="post" action="{{$.ItemBase}}{{.ID}}/untake">
        <p class="dim"><span class="inert">Taken</span> {{.CopiesLeft}} copies</p>
        <button class="linkish" type="submit">Put it back</button>
      </form>
    {{else if .AtLimit}}
      <p class="dim"><span class="inert">Take</span> {{.CopiesLeft}} copies</p>
      <p class="hint">You've taken three things with this scan.</p>
    {{else}}
      <form method="post" action="{{$.ItemBase}}{{.ID}}/take">
        <button type="submit">Take</button>
        <span class="dim">{{.CopiesLeft}} copies</span>
      </form>
    {{end}}
  {{end}}
</div>
```

Mirror the same block in `item.html`. If a `{{define}}` of the block is shared, note that Milestone 1's template collision is a live hazard: page templates are parsed into separate sets, so a shared block must be added to every set that uses it.

- [ ] **Step 8: Run them and watch them pass**

Run: `go test ./internal/web`
Expected: PASS.

- [ ] **Step 9: Run the whole suite and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/web/take.go internal/web/take_test.go internal/web/routes.go internal/web/shelf.go internal/web/templates/
git commit -m "feat: take and put-it-back on the shelf"
```

---

## Task 7: Sweeps on the read paths, and the leave limit

**Files:**
- Modify: `internal/web/shelf.go`, `internal/web/item.go`, `internal/web/leave.go`
- Modify: `internal/store/session.go`
- Modify: `internal/store/item.go` (`CreateItem` is called by the leave handler; the counter increments beside it)
- Test: `internal/web/sweep_test.go`, `internal/store/session_test.go`

**Interfaces:**
- Consumes: `SweepExpiredSessions`, `SweepExpiredItems` from Task 2.
- Produces:
  - `func (s *Store) LeavesForSession(ctx context.Context, sessionID boulevard.SessionID) (int, error)`
  - `func (s *Store) IncrementLeaves(ctx context.Context, sessionID boulevard.SessionID) error`

- [ ] **Step 1: Write the failing tests**

In `internal/web/sweep_test.go`:

```go
func TestShelfSweepsBeforeItRenders(t *testing.T) {
	// An item shelved 200 days ago on a 90-day library must not appear.
	srv, lib, _ := expiredItemServer(t)

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "long expired") {
		t.Error("the shelf rendered an item past max_age_days")
	}
}

func TestItemPageSweepsToo(t *testing.T) {
	// The gap that is easy to miss: an unswept item is still `shelved`, so
	// the item handler's state filter passes it and the shareable URL
	// outlives the shelf listing.
	srv, lib, itemID := expiredItemServer(t)

	req := httptest.NewRequest(http.MethodGet, "/b/"+lib.Slug+"/i/"+itemID, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — an expired item still serves at its own URL", rec.Code)
	}
}
```

In `internal/store/session_test.go`:

```go
func TestLeavesCounterRoundTrips(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	sess := boulevard.Session{
		ID: "S1", LibraryID: lib.ID, TokenID: "T1",
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	n, err := st.LeavesForSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("leaves: %v", err)
	}
	if n != 0 {
		t.Fatalf("new session has %d leaves, want 0", n)
	}

	for i := 0; i < 2; i++ {
		if err := st.IncrementLeaves(context.Background(), sess.ID); err != nil {
			t.Fatalf("increment: %v", err)
		}
	}
	if n, _ = st.LeavesForSession(context.Background(), sess.ID); n != 2 {
		t.Errorf("leaves = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run 'TestShelfSweeps|TestItemPageSweeps|TestLeavesCounter' ./internal/web ./internal/store`
Expected: FAIL.

- [ ] **Step 3: Add the leaves counter to the store**

In `internal/store/session.go`:

```go
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
```

- [ ] **Step 4: Run the sweeps on both read paths**

At the top of `renderShelf` and of `handleItem`, after the library resolves and before anything is queried:

```go
	// Both sweeps, on every HTML read path that can show an item. The item
	// page is easy to forget and would otherwise serve an expired item at
	// its own shareable URL forever: an unswept item is still `shelved`, so
	// the state filter passes it. A link that outlives the shelf listing is
	// exactly what expiry exists to prevent.
	if _, err := s.store.SweepExpiredSessions(r.Context(), s.now()); err != nil {
		log.Printf("sessions not swept: %v", err)
	}
	if _, err := s.store.SweepExpiredItems(r.Context(), lib.ID, s.now()); err != nil {
		log.Printf("items not swept: %v", err)
	}
```

A failed sweep is logged, not fatal: it makes the page slightly stale, and refusing to render a shelf because a housekeeping write failed is the worse outcome.

`Server` carries no logger field — it has `store` and `now func() time.Time` only. Use the standard library's `log.Printf`, which is what `handleItem` already does for a failed `IncrementViews`. Do not introduce a logging dependency or a logger field for this.

- [ ] **Step 5: Enforce the leave limit**

In `handleLeaveSubmit`, after the session check and before `CreateItem`:

```go
	leaves, err := s.store.LeavesForSession(r.Context(), sess.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	if leaves >= maxLeaves {
		// The composed note comes back with the form, for the reason the
		// 403 and 422 paths already do: losing it is this form's worst
		// failure.
		s.renderLeave(w, lib, http.StatusForbidden, in, nil, sess, true)
		return
	}
```

with `const maxLeaves = 3` beside `maxLeaveBody`, and `s.store.IncrementLeaves(r.Context(), sess.ID)` immediately after a successful `CreateItem`. Pass the at-limit state to the template so the form renders inert with:

> You've left three things with this scan. Scan the card again for more.

- [ ] **Step 6: Run them and watch them pass**

Run: `TZ=America/Chicago go test -count=1 ./...`
Expected: every package PASS.

- [ ] **Step 7: Commit**

```bash
go vet ./... && gofmt -l .
git add internal/web/ internal/store/session.go internal/store/session_test.go
git commit -m "feat: sweep on every read path, and hold both rate limits"
```

---

## Task 8: The shed CLI

**Files:**
- Create: `cmd/boulevard/shed.go`
- Modify: `cmd/boulevard/main.go`
- Test: `cmd/boulevard/shed_test.go`

**Interfaces:**
- Consumes: `ShedItems`, `ReshelveItem`, `ReleaseItem`, `ErrShelfFull`, `ErrNotShed` from Task 5; `resolvePrefix` and `boulevard.Sanitize` from Milestone 2.
- Produces: `runShed`, `runReshelve`, `runRelease`, each `func([]string) int`.

- [ ] **Step 1: Wire the subcommands**

In `cmd/boulevard/main.go`, beside `queue` / `approve` / `reject`:

```go
	case "shed":
		return runShed(args)
	case "reshelve":
		return runReshelve(args)
	case "release":
		return runRelease(args)
```

and add all three to the usage text.

- [ ] **Step 2: Write the failing tests**

In `cmd/boulevard/shed_test.go`. These use the helpers `cmd/boulevard` already has — verified present, do not write new ones:

| Helper | Signature | Where |
|---|---|---|
| `cliStore` | `(t, slugs ...string) (string, *store.Store, []boulevard.Library)` | `queue_cli_test.go:23` |
| `leavePending` | `(t, *store.Store, boulevard.Library, id, note string, left time.Time) boulevard.Item` | `queue_cli_test.go:56` |
| `stateOfItem` | `(t, *store.Store, boulevard.Library, itemID string) boulevard.ItemState` | `queue_cli_test.go:69` |
| `captureStdout` | `(t, fn func()) string` | `booklet_test.go:491` |
| `exitOK` | `= 0` | `main.go:10` |

Commands are called directly and return an exit code — `runShed([]string{"--db", path})` — with output captured by `captureStdout`. There is no `runCLI` wrapper; do not invent one.

No existing helper puts an item in the shed, so this file needs its own. **`store.Store` does not export its `*sql.DB`**, and these tests live in `package main`, not `package store` — so unlike the store's own tests they cannot reach the database directly. Do not add an exported accessor to make them able to; a test-only hole in the store's encapsulation is a worse thing to own than a slightly longer fixture.

Build the fixture from exported store methods only: `leavePending` → `ApproveItem` → `SweepExpiredItems` with a clock well past `max_age_days` produces a shed item with reason `expired`, which is what these CLI tests need. If some case turns out to be unreachable that way, say so in your report rather than working around the encapsulation.

The four tests:

```go
func TestShedListsReasonsAndSanitizesNotes(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	// A note that would erase the line above it and draw a forged entry.
	shedOne(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "\x1b[1A\x1b[2Kforged entry")

	out := captureStdout(t, func() {
		if code := runShed([]string{"--db", path}); code != exitOK {
			t.Errorf("shed exit = %d, want %d", code, exitOK)
		}
	})

	if !strings.Contains(out, "expired") {
		t.Errorf("the listing does not name why the item shed: %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("an escape sequence reached the terminal — Sanitize is not applied")
	}
}

func TestReshelveOntoAFullShelfRefusesAndNamesWhy(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := fullShelfWithOneShedItem(t, s, lib)

	out := captureStdout(t, func() {
		if code := runReshelve([]string{"--db", path, it.ID[:4]}); code == exitOK {
			t.Error("reshelve onto a full shelf succeeded")
		}
	})
	if !strings.Contains(out, "full") {
		t.Errorf("the refusal does not say the shelf is full: %q", out)
	}
}

func TestReleaseFromTheShedSaysReleased(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := shedOne(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "a note")

	out := captureStdout(t, func() {
		if code := runRelease([]string{"--db", path, it.ID[:4]}); code != exitOK {
			t.Errorf("release exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "Released.") {
		t.Errorf("output = %q, want it to say Released.", out)
	}
	if got := stateOfItem(t, s, lib, it.ID); got != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got)
	}
}

func TestShedPrefixRefusesAnAmbiguousMatch(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	shedOne(t, s, lib, "AAAA1ZZZZZZZZZZZZZZZZZZZZZ", "first")
	shedOne(t, s, lib, "AAAA2ZZZZZZZZZZZZZZZZZZZZZ", "second")

	out := captureStdout(t, func() {
		if code := runReshelve([]string{"--db", path, "AAAA"}); code == exitOK {
			t.Error("an ambiguous prefix was resolved rather than refused")
		}
	})
	if !strings.Contains(out, "AAAA1") || !strings.Contains(out, "AAAA2") {
		t.Errorf("the refusal does not name both candidates: %q", out)
	}
}
```

Write `shedOne` and `fullShelfWithOneShedItem` yourself in this file, using only exported store methods and the existing helpers above. `shedOne` should produce a shed item whose reason is `expired`; the cleanest route is `leavePending` → `ApproveItem` → `SweepExpiredItems` with a clock well past `max_age_days`.

- [ ] **Step 3: Run them and watch them fail**

Run: `go test -run 'TestShed|TestReshelve|TestRelease|TestAmbiguous' ./cmd/boulevard`
Expected: FAIL — unknown subcommand.

- [ ] **Step 4: Write `cmd/boulevard/shed.go`**

Model it directly on `queue.go`. Requirements it must meet:

- `shed` prints a count line, then one block per item: the four-character handle in brackets, the type, `reason.Label()` and how long ago it shed, then the note, then the footer `boulevard reshelve <id>   boulevard release <id>`. Empty prints `Nothing in the shed for <name>.`
- **Every stranger-supplied string goes through `boulevard.Sanitize`** — note, attribution, payload. This closes a terminal-injection attack where a note containing `\x1b[1A\x1b[2K` erases the line above and draws a forged entry, so the steward reads one item and acts on another. It is not decoration.
- Handles resolve through `resolvePrefix`: any unique prefix, case-insensitive, Crockford-folded, refusing ambiguity by naming every candidate.
- Times are formatted in the server's local zone. The store returns UTC; convert before formatting or the line is hours out.
- `reshelve` on a full shelf prints the count and the capacity, exits non-zero.
- `reshelve`/`release` on an item that is not in the shed prints that it is not in the shed, exits non-zero — `ErrNotShed`, distinct from a mistyped handle.

- [ ] **Step 5: Run them and watch them pass**

Run: `go test ./cmd/boulevard`
Expected: PASS.

- [ ] **Step 6: Run the whole suite and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add cmd/boulevard/
git commit -m "feat: the shed as a CLI"
```

---

## Task 9: DESIGN.md amendments, CLAUDE.md, and the acceptance checklist

**Files:**
- Modify: `DESIGN.md` (§3, §4, §5)
- Modify: `CLAUDE.md`
- Create: `docs/mechanics-acceptance.md`

**Interfaces:**
- Consumes: everything above.
- Produces: documentation only. No code.

- [ ] **Step 1: Amend `DESIGN.md` §3, sessions**

Replace "Sessions are never swept" with the sweep rule, and rewrite the no-link paragraph to distinguish the two cases: a left item never links to a session, because the item is public and permanent; a take links to the session that made it and is deleted with it, which bounds the link to twenty-four hours and makes it not a user record.

- [ ] **Step 2: Amend `DESIGN.md` §3, the item table**

Add `shed_at` and `shed_reason` rows, with the reason's three values.

- [ ] **Step 3: Amend `DESIGN.md` §4, session mechanics**

State that sessions are swept lazily on the read paths, and that expiry is still checked on read — the two are belt and braces, not alternatives.

- [ ] **Step 4: Amend `DESIGN.md` §5, taking and the shed**

Take is one tap that records and opens; `POST` only; undoable while the session lasts; undo and re-shelve refuse a full shelf rather than evicting. Items record how they got to the shed, and the reason informs a human decision and nothing automatic.

- [ ] **Step 5: Update `CLAUDE.md`**

Move Milestone 3 to ✅ in the build order, name what it shipped, and add to the invariants: sessions are swept, so a session-scoped link is bounded — but a leave still must not link, and the asymmetry is deliberate. Also record that take is `POST` only and why.

- [ ] **Step 6: Write `docs/mechanics-acceptance.md`**

A physical checklist in the shape of `docs/shelf-acceptance.md`. It must cover, each as its own `- [ ]`:

- Taking a link opens it and the count drops by one
- The shelf then offers "Put it back", and using it restores the count
- Taking the last copy removes the item from the shelf
- After three takes the control goes inert and explains why
- Scanning the card again restores the three
- After three leaves the form goes inert and explains why
- `boulevard shed` lists the items with their reasons
- `boulevard reshelve <id>` puts one back; `boulevard release <id>` does not
- A pinned item shows no take control at all
- An expired item is gone from both the shelf **and** its own URL

Include the same "Run record" section shelf-acceptance.md has, and leave it saying the run has not been performed. Do not tick boxes no one has performed.

- [ ] **Step 7: Commit**

```bash
git add DESIGN.md CLAUDE.md docs/mechanics-acceptance.md
git commit -m "docs: the spec matches the code again"
```

---

## Self-Review

**Spec coverage.** §2 sweeping → Tasks 2, 7. §3 take → Tasks 4, 6. §4 undo → Tasks 4, 6. §5 expiry → Tasks 2, 7. §6 the shed → Tasks 1, 3, 5, 8. §7 rate limits → Tasks 4, 7. §8 pins → Tasks 2, 4, 6. §9 schema → Task 1. §10 surfaces → Task 6. §11 amendments → Task 9. §12 testing → distributed, with the non-UTC clock in the Global Constraints. §13 out of scope → nothing to build.

**Type consistency.** `boulevard.ShedReason` and its four constants are defined in Task 1 and used in 2, 3, 4, 5, 8. `ErrLimitReached` / `ErrNoCopiesLeft` / `ErrShelfFull` are defined in Task 4 step 1 and used in 4, 5, 6, 8; `ErrNotShed` in Task 5 step 4, used in 5 and 8. `TakeItem` takes `maxTakes int` in Task 4 and Task 6 passes the `maxTakes` constant. `TakenBySession` returns `map[string]bool` in Task 4 and Task 6 indexes it.

**Four gaps found and closed.** `ErrNotShed` was used in Task 5's implementation before being defined; its definition is now step 4 of that task. Three references named things the codebase does not have: `shelfPath(lib)` takes a slug, not a `Library` — corrected to `shelfURL(lib)`; `Server` has no logger field, so the sweeps use `log.Printf` as `handleItem` already does; and `ItemBase`/`itemURL` did not exist at all, so Task 6 now creates them rather than assuming them.

**Not verified, and the implementer must check:** Task 8's test fixtures are described as reusing helpers from `cmd/boulevard/queue_cli_test.go` (`runCLI`, `runCLIWithCode` and the database builders) without those names having been confirmed against that file. Read it first and match whatever it actually exports; do not add a parallel set of helpers that does the same job under different names.
