# Milestone 4a — the steward's desk Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the steward a web admin behind a generated key — hub, approval queue, shelf management with pins, the shed, and settings.

**Architecture:** A generated 128-bit key, stored only as a SHA-256 hash on the library row and compared in constant time, mints a thirty-day session in its own table so Milestone 3's unconditional presence sweep can never touch it. Admin lives under `/b/{slug}/steward/` and calls the same store methods the existing CLIs call, so nothing is implemented twice.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (pure Go — cgo would break the single-binary install), stdlib `net/http` pattern routing, `html/template` behind `go:embed`, `crypto/sha256` and `crypto/subtle` from the standard library.

**Spec:** `docs/superpowers/specs/2026-08-17-steward-desk-design.md`. Read the relevant section before implementing.

## Global Constraints

- **No new dependencies.** The binary must stay a one-file download. Nothing outside the standard library plus the three modules already in `go.mod`. In particular: **do not add `golang.org/x/crypto`** — the key is a generated 128-bit secret, not a chosen password, so `crypto/sha256` is correct and a KDF is not needed.
- **Clocks are injected, never read in place.** Store methods take `now time.Time`; handlers use `s.now()`. No `time.Now()` in either. `cmd/boulevard` is the composition root and may call it.
- **Randomness is injected.** `boulevard.RandomBase32(r io.Reader, nBytes int)` takes a reader so tests are deterministic; production passes `crypto/rand.Reader`. `boulevard.EntropyBytes` is 16.
- **The store round-trips timestamps through RFC3339 in UTC.** Write `t.UTC().Format(time.RFC3339)`, parse with `time.Parse(time.RFC3339, s)`.
- **Use a non-UTC clock in any test that formats or compares a time.** `chicago(t)` exists in `internal/store/sweep_test.go`. A test pinning UTC on both sides passes while the real thing is a day out; this has shipped once.
- **Never hold an open `*sql.Rows` while issuing another query.** `SetMaxOpenConns(1)` means one unclosed `Rows` owns the only connection and the next query blocks forever — a hang, not an error. Use `QueryRow` for single rows; `defer rows.Close()` immediately where `Query` is unavoidable.
- **Migration columns live only in the migration, never also in `schema.sql`.** A fresh database runs both and fails on "duplicate column name".
- **No store method infers a current library.** Every method takes an explicit `boulevard.LibraryID`, except resolution boundaries (`LibraryBySlug`, `TokenBySecret`, `SessionByID`, `DeleteSession`, and the new `StewardSessionByID`/`DeleteStewardSession`) and host-scoped queries (`LibrarySlugs`, `SweepExpiredSessions`).
- **Every steward mutation is `POST`.** A `GET` mutation is shareable, prefetchable by a browser or link scanner, and triggerable by anything that renders a URL.
- **Steward routes 404 when no key is set** — every route, including the login form. Not 403.
- **`steward_key_hash` is never a field on `boulevard.Library`.** It is read and written only by the dedicated store methods, so `UpdateLibrary` cannot blank it.
- **Test-package helpers that exist and must be reused, not rewritten:** `openTemp(t) *Store`, `makeLibrary(t) boulevard.Library`, `seededLibrary(t, s) (boulevard.Library, []boulevard.Token)` in `internal/store/store_test.go`; `seedLibrary(t, st)`, `chicago(t)`, `shelvedTestItem`, `shelvedTestItemAt` in `internal/store/sweep_test.go`; `pendingTestItem` in `internal/store/item_test.go`; `cliStore`, `leavePending`, `stateOfItem` in `cmd/boulevard/queue_cli_test.go`; `captureStdout`, `captureStderr` in `cmd/boulevard/booklet_test.go`.
- **Any test creating a `boulevard.Session` must use `seededLibrary` and a real token id from it** — `sessions.token_id` is an enforced foreign key. Steward sessions have no such constraint.
- `go vet ./...` and `gofmt -l .` must both be clean before every commit.
- Run `go test ./...` **and** `TZ=America/Chicago go test -count=1 ./...` in the foreground. The suite takes about five seconds — do not set up a monitor or background watcher to wait on it.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/boulevard/steward.go` | key generation, hashing, constant-time match | create |
| `internal/boulevard/item.go` | `ShedRemoved` + its `Label()` case | modify |
| `internal/store/migrate.go` | migration 5 | modify |
| `internal/store/steward.go` | key set/verify, steward session lifecycle | create |
| `internal/store/item.go` | `ErrAllPinned` in `ApproveItem`; `PinItem`, `UnpinItem`, `RemoveItem` | modify |
| `internal/store/store.go` | `ErrAllPinned`, `ErrNotShelved`, `ErrPinLimit` | modify |
| `internal/web/steward.go` | `stewardData`, auth gate, login, logout, hub | create |
| `internal/web/steward_items.go` | queue, shelf, shed pages and their POST actions | create |
| `internal/web/steward_settings.go` | the settings form | create |
| `internal/web/render.go` | stamp `stewardData` with the AGPL footer fields | modify |
| `internal/web/server.go` | register the steward templates | modify |
| `internal/web/routes.go` | the steward routes | modify |
| `internal/web/templates/steward-*.html` | six admin pages | create |
| `cmd/boulevard/stewardkey.go` | the `steward-key` command | create |
| `cmd/boulevard/main.go` | wire `steward-key` | modify |
| `cmd/boulevard/queue.go` | render `ErrAllPinned` | modify |
| `cmd/boulevard/serve.go` | startup warnings | modify |

`internal/web/shelf.go` is already the largest file in that package at 265 lines. The admin goes in three new files rather than into it.

---

## Task 1: The key, the reason, and migration 5

**Files:**
- Create: `internal/boulevard/steward.go`, `internal/boulevard/steward_test.go`
- Modify: `internal/boulevard/item.go`, `internal/store/migrate.go`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: `boulevard.RandomBase32(r io.Reader, nBytes int) (string, error)`, `boulevard.EntropyBytes` (= 16).
- Produces: `boulevard.NewStewardKey(r io.Reader) (string, error)`; `boulevard.HashStewardKey(key string) string`; `boulevard.StewardKeyMatches(hash, key string) bool`; `boulevard.ShedRemoved`; migration version 5 adding `libraries.steward_key_hash` and the `steward_sessions` table.

- [ ] **Step 1: Write the failing key tests**

Create `internal/boulevard/steward_test.go`:

```go
package boulevard

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewStewardKeyIsCrockfordAndFullEntropy(t *testing.T) {
	key, err := NewStewardKey(bytes.NewReader(make([]byte, EntropyBytes)))
	if err != nil {
		t.Fatalf("NewStewardKey: %v", err)
	}
	// 16 bytes of base32 with no padding is 26 characters.
	if len(key) != 26 {
		t.Errorf("len = %d, want 26", len(key))
	}
	for _, r := range key {
		if !strings.ContainsRune(CrockfordAlphabet, r) {
			t.Errorf("key contains %q, which is outside the Crockford alphabet", r)
		}
	}
}

func TestStewardKeyMatchesOnlyTheRightKey(t *testing.T) {
	hash := HashStewardKey("K7Q93ZDMXR148PVBTN5A6CQXW2")

	if !StewardKeyMatches(hash, "K7Q93ZDMXR148PVBTN5A6CQXW2") {
		t.Error("the correct key did not match its own hash")
	}
	if StewardKeyMatches(hash, "K7Q93ZDMXR148PVBTN5A6CQXW3") {
		t.Error("a different key matched")
	}
	if StewardKeyMatches(hash, "") {
		t.Error("the empty key matched")
	}
}

// An empty hash is the "no key set" state. Nothing may authenticate against
// it — including the empty string, which is what a form submitted with no
// value would send.
func TestEmptyHashMatchesNothing(t *testing.T) {
	for _, key := range []string{"", "K7Q93ZDMXR148PVBTN5A6CQXW2"} {
		if StewardKeyMatches("", key) {
			t.Errorf("empty hash matched %q", key)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/boulevard`
Expected: FAIL — `undefined: NewStewardKey`.

- [ ] **Step 3: Write `internal/boulevard/steward.go`**

```go
package boulevard

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
)

// NewStewardKey generates the steward's credential (DESIGN.md §6, §8).
//
// It is generated, never chosen. §8 already implies this — "prints the
// steward password once" is the behaviour of something generated — and the
// distinction settles two things at once. A chosen password would need a
// real KDF, and therefore a dependency this project does not carry, because
// chosen passwords are low-entropy and reused across sites so a leak harms
// the steward elsewhere. A generated 128-bit secret has neither problem.
//
// Same alphabet and length as a token secret, for the reason §4 gives: a
// value read off paper must not be mistypeable into a different valid one.
func NewStewardKey(r io.Reader) (string, error) {
	k, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new steward key: %w", err)
	}
	return k, nil
}

// HashStewardKey is what gets stored. Only the hash is kept.
//
// This is not the same decision as §4's deliberately plaintext token
// secrets, and the difference is worth stating because the two rules
// otherwise look contradictory. A token secret stays recoverable because
// reprinting must always work — a lost booklet is likelier than server
// compromise. A steward key has no artifact to reprint: losing it means
// generating another, which costs nothing, so the recoverable form is not
// worth keeping.
//
// Plain SHA-256 with no salt is correct for a 128-bit random input. Salting
// defends against precomputation across many low-entropy secrets; there is
// no precomputing a 128-bit random value.
func HashStewardKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// StewardKeyMatches compares in constant time.
//
// An empty hash is the "no key set" state and must match nothing at all,
// including the empty key a blank form would submit — otherwise a fresh
// install would admit anyone who pressed the button.
func StewardKeyMatches(hash, key string) bool {
	if hash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashStewardKey(key))) == 1
}
```

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/boulevard`
Expected: PASS.

- [ ] **Step 5: Add the fourth shed reason**

In `internal/boulevard/item.go`, add to the `ShedReason` constant block:

```go
	ShedRemoved ShedReason = "removed" // the steward took it off the shelf
```

and to `Label()`'s switch, before `default`:

```go
	case ShedRemoved:
		return "you took it down"
```

The other three reasons are mechanical; this is the only one with a person behind it, and a steward reading the shed a week later needs to tell what they chose to remove from what the shelf shed on its own.

- [ ] **Step 6: Write the failing migration test**

In `internal/store/migrate_test.go`:

```go
func TestMigration5AddsStewardKeyAndSessions(t *testing.T) {
	st := openTemp(t)

	var version int
	if err := st.db.QueryRow(
		`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version < 5 {
		t.Fatalf("schema version = %d, want at least 5", version)
	}

	for _, q := range []string{
		`SELECT steward_key_hash FROM libraries LIMIT 1`,
		`SELECT id, library_id, created_at, expires_at FROM steward_sessions LIMIT 1`,
	} {
		// Close each Rows before the next query. SetMaxOpenConns(1) means an
		// unclosed Rows owns the only connection and the next query hangs.
		rows, err := st.db.Query(q)
		if err != nil {
			t.Errorf("%s: %v", q, err)
			continue
		}
		rows.Close()
	}
}

// The default is what makes every existing database correct without a data
// migration, and it is also the "no key set" state that 404s the admin.
func TestStewardKeyHashDefaultsToEmpty(t *testing.T) {
	st := openTemp(t)
	lib := seedLibrary(t, st)

	var hash string
	if err := st.db.QueryRow(
		`SELECT steward_key_hash FROM libraries WHERE id = ?`,
		string(lib.ID)).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if hash != "" {
		t.Errorf("hash = %q, want empty on a fresh library", hash)
	}
}
```

- [ ] **Step 7: Run it and watch it fail**

Run: `go test -run 'TestMigration5|TestStewardKeyHashDefaults' ./internal/store`
Expected: FAIL — `no such column: steward_key_hash`.

- [ ] **Step 8: Add migration 5**

In `internal/store/migrate.go`, after `addTakesAndShedReasons`:

```go
// addStewardCredential is Milestone 4a.
//
// steward_sessions is deliberately its own table rather than a kind column
// on `sessions`. Milestone 3 sweeps `sessions` unconditionally on every
// shelf and item render, and that sweep's value depends on having no
// exceptions — a steward session living there would either be deleted by it
// or force one. They are also different objects: a presence session carries
// the token_id of the card that minted it and exists to be forgotten; a
// steward session has no card behind it and is meant to last thirty days.
//
// An empty steward_key_hash is the "no key set" state, which 404s every
// steward route. The column default makes every existing database correct
// with no data migration.
//
// No ON DELETE CASCADE on library_id: nothing deletes a library, and v2's
// storage model gives each library its own file.
const addStewardCredential = `
ALTER TABLE libraries ADD COLUMN steward_key_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS steward_sessions (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES libraries(id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
`
```

and append to the slice:

```go
	{5, addStewardCredential},
```

**Do not add either to `schema.sql`.** A fresh database would run both and fail on "duplicate column name".

**Do not add a `StewardKeyHash` field to `boulevard.Library`, and do not add the column to `scanLibrary`'s SELECT lists.** `UpdateLibrary` writes every column it knows about, so a hash on the struct is a third thing a carelessly-built `Library` could blank — and the one whose loss locks the steward out of their own box. Task 2 reads and writes it through dedicated methods instead. A trap that cannot be sprung beats a trap with a comment on it.

- [ ] **Step 9: Run the whole suite**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: every package PASS, vet silent, gofmt lists nothing.

- [ ] **Step 10: Commit**

```bash
git add internal/boulevard/steward.go internal/boulevard/steward_test.go internal/boulevard/item.go internal/store/migrate.go internal/store/migrate_test.go
git commit -m "feat: the steward key, and a fourth shed reason"
```

---

## Task 2: The key and the session in the store

**Files:**
- Create: `internal/store/steward.go`, `internal/store/steward_test.go`
- Modify: `internal/store/store.go`

**Interfaces:**
- Consumes: `boulevard.HashStewardKey`, `boulevard.StewardKeyMatches`, `boulevard.NewStewardKey` from Task 1; `SweepExpiredSessions` from Milestone 3.
- Produces:
  - `func (s *Store) SetStewardKeyHash(ctx context.Context, id boulevard.LibraryID, hash string) error`
  - `func (s *Store) StewardKeyIsSet(ctx context.Context, id boulevard.LibraryID) (bool, error)`
  - `func (s *Store) VerifyStewardKey(ctx context.Context, id boulevard.LibraryID, key string) (bool, error)`
  - `func (s *Store) CreateStewardSession(ctx context.Context, sess boulevard.StewardSession) error`
  - `func (s *Store) StewardSessionByID(ctx context.Context, id string, now time.Time) (boulevard.StewardSession, error)`
  - `func (s *Store) RenewStewardSession(ctx context.Context, id string, now, expiresAt time.Time) error` — takes the clock and refuses to revive an expired row; see the note below
  - `func (s *Store) DeleteStewardSession(ctx context.Context, id string) error`
  - `func (s *Store) SweepExpiredStewardSessions(ctx context.Context, now time.Time) (int64, error)`
  - `boulevard.StewardSession` and `boulevard.StewardSessionTTL`

- [ ] **Step 1: Add the session type**

Append to `internal/boulevard/steward.go`:

```go
// StewardSessionTTL is how long a steward stays logged in (DESIGN.md §6:
// "sessions long-lived"). Thirty days, because the alternative is a steward
// re-typing a 26-character key at their box in the cold.
const StewardSessionTTL = 30 * 24 * time.Hour

// StewardSession is one logged-in steward.
//
// It carries no token_id, unlike a presence Session: no card mints it, and
// nothing about it is tied to the physical box. It lives in its own table
// for that reason and for a sharper one — Milestone 3's sweep over
// `sessions` must stay unconditional.
type StewardSession struct {
	ID        string
	LibraryID LibraryID
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewStewardSessionID is an opaque 128-bit value, unsigned for the reason §4
// gives for the presence session id: the row is the source of truth, so a
// signature would add a key to store, rotate and lose while preventing no
// forgery the lookup already rejects.
func NewStewardSessionID(r io.Reader) (string, error) {
	s, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new steward session id: %w", err)
	}
	return s, nil
}
```

Add `"time"` to that file's imports.

- [ ] **Step 2: Write the failing store tests**

Create `internal/store/steward_test.go`:

```go
package store

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func stewardSetup(t *testing.T) (*Store, boulevard.Library, time.Time) {
	t.Helper()
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	return st, lib, time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
}

func TestStewardKeyRoundTrips(t *testing.T) {
	st, lib, _ := stewardSetup(t)
	ctx := context.Background()

	set, err := st.StewardKeyIsSet(ctx, lib.ID)
	if err != nil {
		t.Fatalf("is set: %v", err)
	}
	if set {
		t.Fatal("a fresh library reports a key already set")
	}

	key, err := boulevard.NewStewardKey(rand.Reader)
	if err != nil {
		t.Fatalf("new key: %v", err)
	}
	if err := st.SetStewardKeyHash(ctx, lib.ID,
		boulevard.HashStewardKey(key)); err != nil {
		t.Fatalf("set: %v", err)
	}

	if set, _ = st.StewardKeyIsSet(ctx, lib.ID); !set {
		t.Error("key not reported as set after storing one")
	}

	ok, err := st.VerifyStewardKey(ctx, lib.ID, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("the correct key did not verify")
	}

	if ok, _ = st.VerifyStewardKey(ctx, lib.ID, "WRONGWRONGWRONGWRONGWRONGW"); ok {
		t.Error("a wrong key verified")
	}
}

// The plaintext key must never reach the database.
func TestOnlyTheHashIsStored(t *testing.T) {
	st, lib, _ := stewardSetup(t)
	key := "K7Q93ZDMXR148PVBTN5A6CQXW2"
	if err := st.SetStewardKeyHash(context.Background(), lib.ID,
		boulevard.HashStewardKey(key)); err != nil {
		t.Fatalf("set: %v", err)
	}

	var stored string
	if err := st.db.QueryRow(
		`SELECT steward_key_hash FROM libraries WHERE id = ?`,
		string(lib.ID)).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored == key {
		t.Fatal("the plaintext key is in the database")
	}
	if stored != boulevard.HashStewardKey(key) {
		t.Error("stored value is neither the key nor its hash")
	}
}

func TestStewardSessionRoundTripsAndExpires(t *testing.T) {
	st, lib, now := stewardSetup(t)
	ctx := context.Background()

	sess := boulevard.StewardSession{
		ID: "STEWARD1", LibraryID: lib.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.StewardSessionTTL),
	}
	if err := st.CreateStewardSession(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.StewardSessionByID(ctx, sess.ID, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.LibraryID != lib.ID {
		t.Errorf("library = %q, want %q", got.LibraryID, lib.ID)
	}

	// Expiry is checked on read, exactly as presence sessions are — the
	// sweep is traffic-driven, so the read check is what makes the interval
	// between sweeps safe.
	if _, err := st.StewardSessionByID(ctx, sess.ID,
		now.Add(boulevard.StewardSessionTTL).Add(time.Minute)); err == nil {
		t.Error("an expired steward session was returned")
	}
}

// The trap this whole table exists to avoid. Milestone 3 sweeps `sessions`
// on every shelf and item render; a steward session must be untouched by it.
func TestPresenceSweepDoesNotDeleteAStewardSession(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	dead := boulevard.Session{
		ID: "DEAD", LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := st.CreateSession(ctx, dead); err != nil {
		t.Fatalf("create presence session: %v", err)
	}
	steward := boulevard.StewardSession{
		ID: "STEWARD1", LibraryID: lib.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.StewardSessionTTL),
	}
	if err := st.CreateStewardSession(ctx, steward); err != nil {
		t.Fatalf("create steward session: %v", err)
	}

	if _, err := st.SweepExpiredSessions(ctx, now); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := st.StewardSessionByID(ctx, steward.ID, now); err != nil {
		t.Fatalf("the presence sweep ate the steward session: %v", err)
	}
}

func TestSweepExpiredStewardSessions(t *testing.T) {
	st, lib, now := stewardSetup(t)
	ctx := context.Background()

	live := boulevard.StewardSession{
		ID: "LIVE", LibraryID: lib.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	dead := boulevard.StewardSession{
		ID: "DEAD", LibraryID: lib.ID,
		CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	for _, s := range []boulevard.StewardSession{live, dead} {
		if err := st.CreateStewardSession(ctx, s); err != nil {
			t.Fatalf("create %s: %v", s.ID, err)
		}
	}

	n, err := st.SweepExpiredStewardSessions(ctx, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d, want 1", n)
	}
	if _, err := st.StewardSessionByID(ctx, live.ID, now); err != nil {
		t.Errorf("the live session was swept: %v", err)
	}
}

func TestRenewAndDeleteStewardSession(t *testing.T) {
	st, lib, now := stewardSetup(t)
	ctx := context.Background()
	sess := boulevard.StewardSession{
		ID: "STEWARD1", LibraryID: lib.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateStewardSession(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	later := now.Add(boulevard.StewardSessionTTL)
	if err := st.RenewStewardSession(ctx, sess.ID, later); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if _, err := st.StewardSessionByID(ctx, sess.ID, now.Add(2*time.Hour)); err != nil {
		t.Errorf("session expired despite renewal: %v", err)
	}

	if err := st.DeleteStewardSession(ctx, sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.StewardSessionByID(ctx, sess.ID, now); err == nil {
		t.Error("a deleted session was returned")
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `go test -run 'TestSteward|TestOnlyTheHash|TestPresenceSweepDoes|TestSweepExpiredSteward|TestRenewAndDelete' ./internal/store`
Expected: FAIL — `st.StewardKeyIsSet undefined`.

- [ ] **Step 4: Write `internal/store/steward.go`**

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
```

- [ ] **Step 5: Run them and watch them pass**

Run: `go test -run 'TestSteward|TestOnlyTheHash|TestPresenceSweepDoes|TestSweepExpiredSteward|TestRenewAndDelete' ./internal/store`
Expected: PASS, all six.

- [ ] **Step 6: Run the whole suite under a non-UTC zone and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/boulevard/steward.go internal/store/steward.go internal/store/steward_test.go
git commit -m "feat: the steward key and its session, in their own table"
```

---

## Task 3: Pins, remove, and the branch pinning switches on

Read spec §6 before starting. The third piece here is the important one.

**Files:**
- Modify: `internal/store/item.go`, `internal/store/store.go`
- Test: `internal/store/item_test.go`

**Interfaces:**
- Consumes: `boulevard.ShedRemoved` from Task 1.
- Produces:
  - `func (s *Store) PinItem(ctx context.Context, id boulevard.LibraryID, itemID string, maxPins int) error`
  - `func (s *Store) UnpinItem(ctx context.Context, id boulevard.LibraryID, itemID string) error`
  - `func (s *Store) RemoveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) error`
  - sentinels `ErrAllPinned`, `ErrNotShelved`, `ErrPinLimit`
  - `ApproveItem` gains an `ErrAllPinned` return

- [ ] **Step 1: Add the three sentinels**

In `internal/store/store.go`, beside the existing ones:

```go
	// ErrAllPinned is a full shelf with nothing evictable. ApproveItem's
	// comment has described this since Milestone 2 and called it
	// unreachable "today, because no code sets pinned" — Milestone 4a is
	// when it can fire. The answer is to refuse the approval naming the
	// pins, never to evict one: a pin is the steward saying "this stays".
	ErrAllPinned = errors.New("every item on the shelf is pinned")

	// ErrNotShelved distinguishes "no such item" from "that item is not on
	// the shelf", the way ErrNotPending and ErrNotShed already do for the
	// queue and the shed.
	ErrNotShelved = errors.New("item is not on the shelf")

	// ErrPinLimit is the fourth pin. DESIGN.md §5 caps pins at 3.
	ErrPinLimit = errors.New("pin limit reached")
```

- [ ] **Step 2: Write the failing tests**

In `internal/store/item_test.go`:

```go
// TestApproveRefusesWhenEveryShelvedItemIsPinned covers the branch that
// Milestone 4a switches on. Before pins could be set, ApproveItem's eviction
// query could never match nothing; now it can, and the answer is a refusal
// rather than evicting a pin.
func TestApproveRefusesWhenEveryShelvedItemIsPinned(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	if _, err := st.db.Exec(`UPDATE libraries SET slots = 1 WHERE id = ?`,
		string(lib.ID)); err != nil {
		t.Fatalf("set slots: %v", err)
	}
	pinned := shelvedTestItemAt(t, st, lib.ID, "PINNED", now.Add(-time.Hour))
	if err := st.PinItem(ctx, lib.ID, pinned.ID, 3); err != nil {
		t.Fatalf("pin: %v", err)
	}
	waiting := pendingTestItem(t, st, lib.ID, "WAITING", now)

	_, err := st.ApproveItem(ctx, lib.ID, waiting.ID, now)
	if !errors.Is(err, ErrAllPinned) {
		t.Fatalf("approve error = %v, want ErrAllPinned", err)
	}

	// Nothing moved: not the pin, and not the item that was refused.
	got, err := st.ItemByID(ctx, lib.ID, pinned.ID)
	if err != nil {
		t.Fatalf("read pinned: %v", err)
	}
	if got.State != boulevard.ItemShelved {
		t.Errorf("the pinned item was evicted: state = %s", got.State)
	}
	still, err := st.ItemByID(ctx, lib.ID, waiting.ID)
	if err != nil {
		t.Fatalf("read waiting: %v", err)
	}
	if still.State != boulevard.ItemPending {
		t.Errorf("the refused item changed state to %s", still.State)
	}
}

func TestPinLimitIsThree(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	for i, id := range []string{"P1", "P2", "P3"} {
		it := shelvedTestItemAt(t, st, lib.ID, id, now.Add(time.Duration(i)*time.Minute))
		if err := st.PinItem(ctx, lib.ID, it.ID, 3); err != nil {
			t.Fatalf("pin %s: %v", id, err)
		}
	}
	fourth := shelvedTestItemAt(t, st, lib.ID, "P4", now)

	if err := st.PinItem(ctx, lib.ID, fourth.ID, 3); !errors.Is(err, ErrPinLimit) {
		t.Fatalf("fourth pin error = %v, want ErrPinLimit", err)
	}
	got, err := st.ItemByID(ctx, lib.ID, fourth.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Pinned {
		t.Error("the fourth item was pinned anyway")
	}
}

func TestUnpinFreesASlotForAnotherPin(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	first := shelvedTestItemAt(t, st, lib.ID, "P1", now)
	if err := st.PinItem(ctx, lib.ID, first.ID, 1); err != nil {
		t.Fatalf("pin: %v", err)
	}
	second := shelvedTestItemAt(t, st, lib.ID, "P2", now)
	if err := st.PinItem(ctx, lib.ID, second.ID, 1); !errors.Is(err, ErrPinLimit) {
		t.Fatalf("second pin error = %v, want ErrPinLimit", err)
	}

	if err := st.UnpinItem(ctx, lib.ID, first.ID); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if err := st.PinItem(ctx, lib.ID, second.ID, 1); err != nil {
		t.Fatalf("pin after unpin: %v", err)
	}
}

func TestRemoveShedsWithItsOwnReason(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()

	it := shelvedTestItemAt(t, st, lib.ID, "ITEM1", now)
	if err := st.RemoveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := st.ItemByID(ctx, lib.ID, it.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.State != boulevard.ItemShed {
		t.Errorf("state = %s, want shed — remove sheds, it does not release", got.State)
	}
	if got.ShedReason != boulevard.ShedRemoved {
		t.Errorf("reason = %q, want %q", got.ShedReason, boulevard.ShedRemoved)
	}
	if got.ShedAt == nil {
		t.Error("shed_at was not set")
	}

	// Recoverable, which is why remove sheds rather than releases.
	if err := st.ReshelveItem(ctx, lib.ID, it.ID, now); err != nil {
		t.Errorf("a removed item could not be re-shelved: %v", err)
	}
}

func TestPinUnpinRemoveRefuseItemsNotOnTheShelf(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	lib := seedLibrary(t, st)
	now := time.Date(2026, 8, 17, 20, 25, 0, 0, loc)
	ctx := context.Background()
	pending := pendingTestItem(t, st, lib.ID, "WAITING", now)

	if err := st.PinItem(ctx, lib.ID, pending.ID, 3); !errors.Is(err, ErrNotShelved) {
		t.Errorf("pin error = %v, want ErrNotShelved", err)
	}
	if err := st.UnpinItem(ctx, lib.ID, pending.ID); !errors.Is(err, ErrNotShelved) {
		t.Errorf("unpin error = %v, want ErrNotShelved", err)
	}
	if err := st.RemoveItem(ctx, lib.ID, pending.ID, now); !errors.Is(err, ErrNotShelved) {
		t.Errorf("remove error = %v, want ErrNotShelved", err)
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

Run: `go test -run 'TestApproveRefusesWhenEvery|TestPin|TestUnpin|TestRemoveSheds' ./internal/store`
Expected: FAIL — `st.PinItem undefined`.

- [ ] **Step 4: Make `ApproveItem` refuse an all-pinned shelf**

In `internal/store/item.go`, the eviction block currently scans the oldest non-pinned id and lets a `sql.ErrNoRows` fall through as an unhelpful wrapped error. Replace that scan's error handling:

```go
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM items
			  WHERE library_id = ? AND state = 'shelved' AND pinned = 0
			  ORDER BY shelved_at ASC, id ASC LIMIT 1`,
			string(id)).Scan(&evicted)
		if errors.Is(err, sql.ErrNoRows) {
			// Every shelved item is pinned. Refuse rather than evict: a pin
			// is the steward saying "this stays", and silently overriding it
			// is exactly the surprise §5 warns generates steward labour.
			return "", fmt.Errorf("shelf is full and every item is pinned: %w", ErrAllPinned)
		}
		if err != nil {
			return "", fmt.Errorf("find the oldest shelved item: %w", err)
		}
```

Replace the comment above that block — it currently says the case cannot fire — with one recording that Milestone 4a made it reachable.

- [ ] **Step 5: Add the three methods**

Append to `internal/store/item.go`:

```go
// PinItem marks a shelved item as furniture: never evicted, never expired,
// not takeable (DESIGN.md §5). maxPins is passed in rather than read from
// settings because §5 fixes it at 3 for every library.
//
// The count and the write share one transaction. Two stewards pinning at
// once through separate processes would otherwise both see two pins and
// both write a third.
func (s *Store) PinItem(ctx context.Context, id boulevard.LibraryID, itemID string, maxPins int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pin: %w", err)
	}
	defer tx.Rollback()

	var state string
	var pinned int
	err = tx.QueryRowContext(ctx,
		`SELECT state, pinned FROM items WHERE library_id = ? AND id = ?`,
		string(id), itemID).Scan(&state, &pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("look up item %q: %w", itemID, err)
	}
	if boulevard.ItemState(state) != boulevard.ItemShelved {
		return fmt.Errorf("item %q is %s: %w", itemID, state, ErrNotShelved)
	}
	if pinned == 1 {
		return nil // already pinned; nothing to do
	}

	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE library_id = ? AND state = 'shelved' AND pinned = 1`,
		string(id)).Scan(&count); err != nil {
		return fmt.Errorf("count pins: %w", err)
	}
	if count >= maxPins {
		return fmt.Errorf("%d of %d pins used: %w", count, maxPins, ErrPinLimit)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET pinned = 1 WHERE library_id = ? AND id = ?`,
		string(id), itemID); err != nil {
		return fmt.Errorf("pin %q: %w", itemID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pin: %w", err)
	}
	return nil
}

// UnpinItem returns an item to ordinary stock: evictable, expirable,
// takeable again.
func (s *Store) UnpinItem(ctx context.Context, id boulevard.LibraryID, itemID string) error {
	return s.setShelvedFlag(ctx, id, itemID, `UPDATE items SET pinned = 0 WHERE library_id = ? AND id = ?`)
}

// RemoveItem is the steward taking something off the shelf. It sheds rather
// than releases, so a misfire is recoverable by re-shelving and `release`
// stays the deliberate second step — the same soft-delete reasoning §5 gives
// for rejection.
func (s *Store) RemoveItem(ctx context.Context, id boulevard.LibraryID, itemID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove: %w", err)
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
	if boulevard.ItemState(state) != boulevard.ItemShelved {
		return fmt.Errorf("item %q is %s: %w", itemID, state, ErrNotShelved)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET state = 'shed', shed_at = ?, shed_reason = ?, pinned = 0
		  WHERE library_id = ? AND id = ?`,
		now.UTC().Format(time.RFC3339), string(boulevard.ShedRemoved),
		string(id), itemID); err != nil {
		return fmt.Errorf("remove %q: %w", itemID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove: %w", err)
	}
	return nil
}

// setShelvedFlag runs a one-column update against a shelved item, reading
// state first so "never existed" and "not on the shelf" stay different
// answers.
func (s *Store) setShelvedFlag(ctx context.Context, id boulevard.LibraryID, itemID, stmt string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update: %w", err)
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
	if boulevard.ItemState(state) != boulevard.ItemShelved {
		return fmt.Errorf("item %q is %s: %w", itemID, state, ErrNotShelved)
	}
	if _, err := tx.ExecContext(ctx, stmt, string(id), itemID); err != nil {
		return fmt.Errorf("update %q: %w", itemID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update: %w", err)
	}
	return nil
}
```

`RemoveItem` clears `pinned` as it sheds: a pinned item that comes back from the shed should return as ordinary stock rather than silently consuming a pin slot while off the shelf.

- [ ] **Step 6: Run them and watch them pass**

Run: `go test -run 'TestApprove|TestPin|TestUnpin|TestRemoveSheds' ./internal/store`
Expected: PASS, including the existing approval tests.

- [ ] **Step 7: Render `ErrAllPinned` in the CLI**

In `cmd/boulevard/queue.go`, the approve path branches on `ErrNotPending`. Add a branch for `ErrAllPinned` that prints the refusal and exits non-zero, in the `  x  %v` shape the file already uses, naming what to do instead:

```
  x  shelf is full and every item is pinned — nothing was approved.
     Unpin something or raise the slot count first.
```

- [ ] **Step 8: Run the whole suite under a non-UTC zone and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/store/item.go internal/store/store.go internal/store/item_test.go cmd/boulevard/queue.go
git commit -m "feat: pins, remove, and the refusal pinning makes reachable"
```

---

## Task 4: The `steward-key` command

**Files:**
- Create: `cmd/boulevard/stewardkey.go`, `cmd/boulevard/stewardkey_test.go`
- Modify: `cmd/boulevard/main.go`

**Interfaces:**
- Consumes: `boulevard.NewStewardKey`, `boulevard.HashStewardKey` from Task 1; `SetStewardKeyHash` from Task 2; `queueLibrary(db, slug string) (*store.Store, boulevard.Library, int)` and `exitOK`/`exitUsage`/`exitIO` from `cmd/boulevard/queue.go` and `main.go`.
- Produces: `runStewardKey(args []string) int`.

- [ ] **Step 1: Write the failing test**

Create `cmd/boulevard/stewardkey_test.go`:

```go
func TestStewardKeyPrintsAKeyOnceAndStoresOnlyItsHash(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]

	out := captureStdout(t, func() {
		if code := runStewardKey([]string{"--db", path}); code != exitOK {
			t.Errorf("exit = %d, want %d", code, exitOK)
		}
	})

	if !strings.Contains(out, "shown once") {
		t.Errorf("output does not say the key is shown once: %q", out)
	}

	// Pull the 26-character Crockford key out of the output and check it
	// verifies — and that what is stored is not the key itself.
	var key string
	for _, f := range strings.Fields(out) {
		if len(f) == 26 {
			key = f
		}
	}
	if key == "" {
		t.Fatalf("no 26-character key in the output: %q", out)
	}
	ok, err := s.VerifyStewardKey(context.Background(), lib.ID, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("the printed key does not verify against what was stored")
	}
}

func TestStewardKeyReplacesAnExistingKey(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	ctx := context.Background()

	first := captureStdout(t, func() { runStewardKey([]string{"--db", path}) })
	second := captureStdout(t, func() { runStewardKey([]string{"--db", path}) })

	keyOf := func(out string) string {
		for _, f := range strings.Fields(out) {
			if len(f) == 26 {
				return f
			}
		}
		return ""
	}
	old, new := keyOf(first), keyOf(second)
	if old == "" || new == "" || old == new {
		t.Fatalf("expected two different keys, got %q and %q", old, new)
	}

	if ok, _ := s.VerifyStewardKey(ctx, lib.ID, old); ok {
		t.Error("the replaced key still verifies")
	}
	if ok, _ := s.VerifyStewardKey(ctx, lib.ID, new); !ok {
		t.Error("the new key does not verify")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run TestStewardKey ./cmd/boulevard`
Expected: FAIL — `undefined: runStewardKey`.

- [ ] **Step 3: Write `cmd/boulevard/stewardkey.go`**

Model it on `runQueue` in `queue.go`: same `flag.NewFlagSet`, same `--db` and `--slug`, same `queueLibrary` for resolution, same exit codes, errors to `os.Stderr` in the `  x  %v` shape.

It generates with `boulevard.NewStewardKey(rand.Reader)`, stores `boulevard.HashStewardKey(key)` via `SetStewardKeyHash`, and prints to stdout:

```
  Your steward key, shown once:

    K7Q93ZDMXR148PVBTN5A6CQXW2

  Write it down. It is not recoverable — run this
  command again to replace it.
```

Nothing else prints the key, and it is never logged.

- [ ] **Step 4: Wire the subcommand**

In `cmd/boulevard/main.go`, beside the existing cases:

```go
	case "steward-key":
		return runStewardKey(args)
```

and add it to the usage text.

- [ ] **Step 5: Run them and watch them pass**

Run: `go test ./cmd/boulevard`
Expected: PASS.

- [ ] **Step 6: Run the whole suite and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add cmd/boulevard/
git commit -m "feat: boulevard steward-key"
```

---

## Task 5: The web auth gate, login and logout

Read spec §3 and §4 before starting.

**Files:**
- Create: `internal/web/steward.go`, `internal/web/steward_test.go`, `internal/web/templates/steward-login.html`
- Modify: `internal/web/render.go`, `internal/web/server.go`, `internal/web/routes.go`

**Interfaces:**
- Consumes: `StewardKeyIsSet`, `VerifyStewardKey`, `CreateStewardSession`, `StewardSessionByID`, `RenewStewardSession`, `DeleteStewardSession`, `SweepExpiredStewardSessions` from Task 2.
- Produces: `stewardData`; `func (s *Server) stewardSession(r *http.Request, lib boulevard.Library) (boulevard.StewardSession, bool)`; `func (s *Server) requireSteward(w http.ResponseWriter, r *http.Request) (boulevard.Library, boulevard.StewardSession, bool)`; `stewardPath(lib boulevard.Library) string`; handlers `handleStewardLogin`, `handleStewardLoginSubmit`, `handleStewardLogout`.

- [ ] **Step 1: Add the page data type and stamp it**

In `internal/web/steward.go`:

```go
// stewardData is the admin's own view model. It is deliberately not
// pageData: the public pages carry items, forms and take-control state that
// mean nothing here, and the admin carries counts and settings that mean
// nothing there. One struct for both would be a grab bag neither page could
// be read against.
type stewardData struct {
	Title       string
	LibraryName string
	ShelfURL    string
	StewardURL  string
	SourceURL   string
	BuildLine   string

	Notice string
	Error  string

	Waiting  int
	Shelved  int
	Slots    int
	Pinned   int
	Shed     int
	ShedWhy  map[string]int

	Items   []boulevard.Item
	Library boulevard.Library
	Errors  map[string]string
}
```

In `internal/web/render.go`, extend the footer stamp so admin pages get it too:

```go
	switch d := data.(type) {
	case pageData:
		d.SourceURL = version.RepoURL
		d.BuildLine = "boulevard " + version.Version + " (" + version.Commit + ")"
		data = d
	case stewardData:
		d.SourceURL = version.RepoURL
		d.BuildLine = "boulevard " + version.Version + " (" + version.Commit + ")"
		data = d
	}
```

- [ ] **Step 2: Register the templates**

In `internal/web/server.go`, add the six admin pages to `pageTemplates`:

```go
	"steward-login.html", "steward-hub.html", "steward-queue.html",
	"steward-shelf.html", "steward-shed.html", "steward-settings.html",
```

They are parsed into their own sets exactly as the public pages are. **Each admin template defines `body`, like every other page** — that is safe only because each page gets its own `template.Must(template.ParseFS(...))` call. Milestone 1 shipped a defect where `ParseFS` over `templates/*.html` collapsed every page's `body` into one; do not consolidate the parsing.

- [ ] **Step 3: Write the failing auth tests**

In `internal/web/steward_test.go`:

```go
// Every steward route 404s until a key exists. Not 403: a fresh install
// should not advertise a surface that is not armed.
func TestStewardRoutes404BeforeAKeyIsSet(t *testing.T) {
	st := testStore(t)
	addLibrary(t, st, "fairview")
	h := New(st, time.Now).Handler()

	for _, path := range []string{
		"/b/fairview/steward/", "/b/fairview/steward/login",
		"/b/fairview/steward/queue", "/b/fairview/steward/shelf",
		"/b/fairview/steward/shed", "/b/fairview/steward/settings",
	} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 before a key is set", path, rec.Code)
		}
	}
}

func TestStewardLoginWithTheRightKeySetsASession(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if cookieNamed(rec, "bl_steward") == nil {
		t.Fatal("no bl_steward cookie was set")
	}
}

func TestStewardLoginWithTheWrongKeyIsRefused(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {"WRONGWRONGWRONGWRONGWRONGW"}}, nil)
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a wrong key logged in")
	}
	if cookieNamed(rec, "bl_steward") != nil {
		t.Error("a cookie was set for a failed login")
	}
	if strings.Contains(rec.Body.String(), "WRONGWRONG") {
		t.Error("the submitted key was echoed back into the page")
	}
}

func TestStewardHubRequiresASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := get(t, h, "/b/"+lib.Slug+"/steward/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want a 303 to the login form", rec.Code)
	}
}

func TestStewardCookieIsHttpOnlyAndStrict(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	rec := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	c := cookieNamed(rec, "bl_steward")
	if c == nil {
		t.Fatal("no cookie")
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("cookie is not SameSite=Strict — every steward route mutates")
	}
}

func TestStewardLogoutEndsTheSession(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()

	login := postForm(t, h, "/b/"+lib.Slug+"/steward/login",
		url.Values{"key": {key}}, nil)
	c := cookieNamed(login, "bl_steward")

	postForm(t, h, "/b/"+lib.Slug+"/steward/logout", url.Values{}, c)

	rec := get(t, h, "/b/"+lib.Slug+"/steward/")
	if rec.Code != http.StatusSeeOther {
		t.Error("the hub still rendered after logout")
	}
}
```

Write `stewardServer(t) (*store.Store, boulevard.Library, string)` in the same file — it builds a store, adds a library, generates a key, stores its hash, and returns the plaintext key. Write `cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie` and `postForm(t, h http.Handler, path string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder` alongside it, unless `internal/web`'s existing test files already provide equivalents — **read `leave_test.go` and `take_test.go` first** and reuse whatever is there rather than adding a parallel set.

- [ ] **Step 4: Run them and watch them fail**

Run: `go test -run TestSteward ./internal/web`
Expected: FAIL — routes 404 because they do not exist.

- [ ] **Step 5: Write the gate and the handlers**

In `internal/web/steward.go`. The gate is the whole task:

```go
const stewardCookieName = "bl_steward"

func stewardPath(lib boulevard.Library) string { return shelfURL(lib) + "steward/" }

// requireSteward resolves the library, refuses if no key is set, and
// requires a live steward session. Every steward handler starts with it.
//
// The order matters. "No key set" 404s before anything else is considered,
// so an unarmed install answers identically whether or not the caller
// guessed a real slug — the same instinct §4 applies to token secrets, where
// unknown and revoked must be byte-identical.
func (s *Server) requireSteward(w http.ResponseWriter, r *http.Request) (boulevard.Library, boulevard.StewardSession, bool) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}
	set, err := s.store.StewardKeyIsSet(r.Context(), lib.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}
	if !set {
		http.NotFound(w, r)
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}

	// Housekeeping on the read path, the pattern Milestone 3 established:
	// no goroutine, no timer, and a failure is logged rather than fatal.
	if _, err := s.store.SweepExpiredStewardSessions(r.Context(), s.now()); err != nil {
		log.Printf("steward sessions not swept: %v", err)
	}

	sess, live := s.stewardSession(r, lib)
	if !live {
		http.Redirect(w, r, stewardPath(lib)+"login", http.StatusSeeOther)
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}
	return lib, sess, true
}
```

`stewardSession` reads the cookie, looks the row up with `s.now()`, checks it belongs to this library, and **renews only when less than half the window remains** — a steward loading five admin pages must not cost five writes through the one connection `SetMaxOpenConns(1)` allows:

```go
	if sess.ExpiresAt.Sub(s.now()) < boulevard.StewardSessionTTL/2 {
		if err := s.store.RenewStewardSession(r.Context(), sess.ID,
			s.now(), s.now().Add(boulevard.StewardSessionTTL)); err != nil {
			log.Printf("steward session not renewed: %v", err)
		}
	}
```

**`RenewStewardSession` takes `now` as well as the new expiry, and its write is conditional on the row not having expired already.** Task 2's review found that an unconditional renewal would extend a session that expired an hour ago but had not yet been swept — and that window always exists, because the sweep is traffic-driven. This call site loads through `StewardSessionByID` first, which already refuses an expired row, so the guard is belt and braces here. It exists so the store does not depend on every future caller remembering a rule it can enforce itself.

A consequence worth knowing at this call site: with the guard in place, `ErrNotFound` from a renewal means *either* "no such id" *or* "already expired". Logging and continuing, as above, is correct for both — the session was already loaded and checked, so a renewal failure is housekeeping, not an auth decision.

The login handler must **not** call `requireSteward` (it would redirect to itself) but must still 404 when no key is set. On success it mints a session, sets the cookie, and 303s to the hub. On failure it re-renders the form with a generic message, sets no cookie, and never echoes the submitted value.

The cookie: `HttpOnly`, `SameSite=Strict`, `Path` scoped to `stewardPath(lib)`, and `Secure` when `strings.HasPrefix(lib.BaseURL, "https://")`.

- [ ] **Step 6: Add the routes**

In `internal/web/routes.go`:

```go
	mux.HandleFunc("GET /b/{slug}/steward/{$}", s.handleStewardHub)
	mux.HandleFunc("GET /b/{slug}/steward/login", s.handleStewardLogin)
	mux.HandleFunc("POST /b/{slug}/steward/login", s.handleStewardLoginSubmit)
	mux.HandleFunc("POST /b/{slug}/steward/logout", s.handleStewardLogout)
```

- [ ] **Step 7: Write `steward-login.html`**

A single password-type field named `key`, a submit button, the library name, and the error line when one is set. It carries the same layout as every other page, so it gets the AGPL footer for free.

- [ ] **Step 8: Run them and watch them pass**

Run: `go test ./internal/web`
Expected: PASS. The hub handler can be a stub returning 200 at this point; Task 6 fills it in.

- [ ] **Step 9: Run the whole suite and commit**

```bash
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/web/ 
git commit -m "feat: the steward's door"
```

---

## Task 6: The hub

**Files:**
- Create: `internal/web/templates/steward-hub.html`
- Modify: `internal/web/steward.go`
- Test: `internal/web/steward_test.go`

**Interfaces:**
- Consumes: `requireSteward`, `stewardData` from Task 5; `PendingItems`, `ShelvedItems`, `ShedItems` from Milestones 2 and 3.
- Produces: `handleStewardHub`.

- [ ] **Step 1: Write the failing tests**

```go
func TestHubCountsWhatNeedsTheSteward(t *testing.T) {
	// two waiting, one shelved, one shed
	st, lib, key, h := stewardServerWithItems(t)
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	body := rec.Body.String()

	if !strings.Contains(body, "Waiting for you") {
		t.Error("no waiting row")
	}
	if !strings.Contains(body, "2") {
		t.Error("the waiting count is not shown")
	}
}

// The state a steward sees most. §6 calls the public empty state arguably
// the most important screen; this is its admin counterpart.
func TestHubSaysNothingNeedsYouWhenNothingDoes(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/", c)
	if !strings.Contains(rec.Body.String(), "Nothing needs you") {
		t.Error("an idle box does not say so")
	}
}
```

Write `stewardServerWithItems`, `loginAsSteward` and `getWithCookie` in the same file, reusing whatever `leave_test.go` and `take_test.go` already provide.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run TestHub ./internal/web`
Expected: FAIL.

- [ ] **Step 3: Write the handler and template**

`handleStewardHub` calls `requireSteward`, then counts from `PendingItems`, `ShelvedItems` and `ShedItems`, tallying pinned items and shed reasons. It renders `steward-hub.html` with `noStore(w)`.

The page shows four rows — waiting, on the shelf (with `N of M slots · K pinned`), in the shed (broken down by reason), and settings — each linking to its page. When all three counts are zero it renders instead:

> **Nothing needs you.**

followed by a one-line summary of the shelf.

- [ ] **Step 4: Run them and watch them pass, then commit**

```bash
go test ./internal/web
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/web/
git commit -m "feat: the hub, and its most common answer"
```

---

## Task 7: Queue, shelf and shed

**Files:**
- Create: `internal/web/steward_items.go`, `internal/web/templates/steward-queue.html`, `steward-shelf.html`, `steward-shed.html`
- Modify: `internal/web/routes.go`
- Test: `internal/web/steward_items_test.go`

**Interfaces:**
- Consumes: `requireSteward`, `stewardData`; `ApproveItem`, `RejectItem`, `PinItem`, `UnpinItem`, `RemoveItem`, `ReshelveItem`, `ReleaseItem`; sentinels `ErrAllPinned`, `ErrPinLimit`, `ErrNotShelved`, `ErrNotShed`, `ErrShelfFull`, `ErrNotPending`, `ErrNotFound`.
- Produces: seven POST handlers and three GET pages.

- [ ] **Step 1: Add the routes**

```go
	mux.HandleFunc("GET /b/{slug}/steward/queue", s.handleStewardQueue)
	mux.HandleFunc("GET /b/{slug}/steward/shelf", s.handleStewardShelf)
	mux.HandleFunc("GET /b/{slug}/steward/shed", s.handleStewardShed)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/approve", s.handleStewardApprove)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/reject", s.handleStewardReject)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/remove", s.handleStewardRemove)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/pin", s.handleStewardPin)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/unpin", s.handleStewardUnpin)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/reshelve", s.handleStewardReshelve)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/release", s.handleStewardRelease)
```

Every mutation is POST, for the reason take is POST-only: a GET mutation is shareable, prefetchable, and triggerable by anything that renders a URL.

- [ ] **Step 2: Write the failing tests**

Cover, each as its own test: approve moves a pending item to the shelf; reject releases it; remove sheds it with reason `removed`; pin then unpin round-trips; a fourth pin renders the limit message naming the three pinned; **approve into an all-pinned full shelf renders the `ErrAllPinned` refusal rather than 500ing**; reshelve returns a shed item; release ends it; and every one of the ten routes redirects to its page rather than rendering a bare body.

The `ErrAllPinned` test is the one that matters — it is the branch this milestone makes reachable, and a handler that forgets it produces a 500 on a legitimate steward action.

- [ ] **Step 3: Write the handlers**

Each POST handler follows one shape: `requireSteward`, read `r.PathValue("id")`, call the store method, branch on the sentinels with `errors.Is`, then `http.Redirect` back to the page it came from with a `Notice` or `Error` carried through. `ErrNotFound` 404s; every other sentinel re-renders its page with a message naming what to do instead.

The three GET pages list their items with the actions that apply: queue gets approve/reject, shelf gets pin/unpin/remove, shed gets reshelve/release. Each shows the note first, as the public shelf does, and each is `noStore(w)`.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/web
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/web/
git commit -m "feat: queue, shelf and shed on the web"
```

---

## Task 8: Settings

Read spec §7 before starting. The read-modify-write is the whole task.

**Files:**
- Create: `internal/web/steward_settings.go`, `internal/web/templates/steward-settings.html`
- Modify: `internal/web/routes.go`
- Test: `internal/web/steward_settings_test.go`

**Interfaces:**
- Consumes: `requireSteward`, `stewardData`; `LibraryByID`, `UpdateLibrary`.
- Produces: `handleStewardSettings`, `handleStewardSettingsSubmit`.

- [ ] **Step 1: Write the failing tests**

```go
// The trap. UpdateLibrary writes nine columns from a whole Library value, so
// a handler that builds one from form input alone silently blanks the slug
// and the base URL — and would pass any test that checked only the six
// fields it meant to change.
func TestSettingsSaveDoesNotBlankTheSlugOrBaseURL(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	postFormWithCookie(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name":     {"A New Name"},
		"location": {"Somewhere else"},
		"slots":    {"7"},
		"max_age":  {"45"},
		"copies":   {"2"},
	}, c)

	got, err := st.LibraryByID(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "A New Name" {
		t.Errorf("name = %q, want the new one", got.Name)
	}
	if got.Slug != lib.Slug {
		t.Errorf("slug = %q, want %q — a rename must not move the URL", got.Slug, lib.Slug)
	}
	if got.BaseURL != lib.BaseURL {
		t.Errorf("base URL = %q, want %q", got.BaseURL, lib.BaseURL)
	}
}

// The steward key hash is not on the Library struct precisely so this cannot
// break — assert it anyway, because it is the failure that locks a steward
// out of their own box.
func TestSettingsSaveDoesNotBreakLogin(t *testing.T) {
	st, lib, key := stewardServer(t)
	h := New(st, time.Now).Handler()
	c := loginAsSteward(t, h, lib, key)

	postFormWithCookie(t, h, "/b/"+lib.Slug+"/steward/settings", url.Values{
		"name": {"A New Name"}, "location": {"x"},
		"slots": {"7"}, "max_age": {"45"}, "copies": {"2"},
	}, c)

	ok, err := st.VerifyStewardKey(context.Background(), lib.ID, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("the steward key stopped working after a settings save")
	}
}

func TestSettingsRejectBadNumbers(t *testing.T) {
	// slots = 0, negative max age, negative copies, blank name: each
	// re-renders the form with an error and changes nothing.
}

func TestLoweringSlotsShedsNothingImmediately(t *testing.T) {
	// Nine shelved, set slots to 5: all nine still shelved afterwards, and
	// the next approval evicts exactly one.
}
```

Fill in the last two bodies following the shape of the first two.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test -run TestSettings ./internal/web`
Expected: FAIL.

- [ ] **Step 3: Write the handler**

The save path **must** read the library first and mutate only the six settable fields:

```go
	lib, err := s.store.LibraryByID(r.Context(), lib.ID)
	// ... then apply only:
	lib.Name = name
	lib.LocationLabel = location
	lib.Slots = slots
	lib.MaxAgeDays = maxAge
	lib.DefaultCopies = copies
	lib.ApprovalRequired = approval
	// ... and write it back with UpdateLibrary.
```

Never construct a `boulevard.Library` from form values.

Validation: name and location non-empty after `strings.TrimSpace`; `slots >= 1`; `max_age >= 0`; `copies >= 0`. On any failure, re-render with the field errors and change nothing.

When the submitted `slots` is below the current shelved count, render the warning above the field:

> The shelf holds 9 items and you are setting 5 slots. Nothing is removed now; the oldest will move to the shed as new items are approved.

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/web
TZ=America/Chicago go test -count=1 ./... && go vet ./... && gofmt -l .
git add internal/web/
git commit -m "feat: settings, read-modify-write"
```

---

## Task 9: The startup warnings, the docs, and the checklist

**Files:**
- Modify: `cmd/boulevard/serve.go`, `DESIGN.md`, `CLAUDE.md`
- Create: `docs/steward-acceptance.md`

- [ ] **Step 1: Warn when no key is set**

In `cmd/boulevard/serve.go`, after the store opens and before the listener starts, check each library and print once:

```
  No steward key set. Run `boulevard steward-key` to enable the admin.
```

- [ ] **Step 2: Warn when the steward key would cross the wire in the clear**

When a library's base URL is not `https://`, print:

```
  ! The base URL is http://calvin.local:8080
    The steward key and its cookie cross the network in the clear.
    Anyone on this network can read them. Put TLS in front of this
    box before it is reachable from outside your LAN.
```

Refusing was considered and rejected: every localhost test and every LAN install would need an override flag, and a flag everyone passes reflexively has stopped being a decision.

- [ ] **Step 3: Amend `DESIGN.md` §6**

Replace "Single password, set at install" with the generated-key rule: a 128-bit key from `boulevard steward-key`, shown once, stored as a hash, replaced rather than recovered, and every steward route 404s until one exists. Add that Remove sheds rather than releases, and that the shed records `removed` as its own reason.

- [ ] **Step 4: Amend `DESIGN.md` §8**

Note that `init` will call `steward-key` rather than implementing key generation itself, so the two cannot drift.

- [ ] **Step 5: Update `CLAUDE.md`**

Move Milestone 4a to ✅ in the build order and say what it shipped. Add `steward-key` to the CLI list. Add to the invariants: the steward key is generated and only its hash is stored — which is not a contradiction of the plaintext token secrets, because a token secret must stay reprintable and a key has no artifact to reprint; steward sessions live in their own table so Milestone 3's presence sweep stays unconditional; and `steward_key_hash` is deliberately absent from `boulevard.Library` so `UpdateLibrary` cannot blank it.

- [ ] **Step 6: Write `docs/steward-acceptance.md`**

In the shape of `docs/mechanics-acceptance.md` — read it first and match its structure, including the "Run record" section. Each item its own `- [ ]`, covering at least:

- Steward routes 404 before `boulevard steward-key` is run
- The key prints once and works; running the command again replaces it and the old one stops working
- The hub says "Nothing needs you" on an idle box, and shows counts when there is work
- Approve and reject from the phone, and the item moves
- Pin an item; it shows no take control on the public shelf; a fourth pin is refused naming the three
- With three pins and three slots, approving something refuses and names the pins rather than evicting one
- Remove sheds an item, and it can be re-shelved
- Settings save changes the name without changing the URL, and the steward stays logged in afterwards
- Lowering slots below the shelved count sheds nothing immediately
- Logging out ends the session; the hub redirects to the login form afterwards
- `serve` warns about the missing key, and about http, exactly once each

**Leave every box unticked and the run record saying the run has not been performed.**

- [ ] **Step 7: Commit**

```bash
git add cmd/boulevard/serve.go DESIGN.md CLAUDE.md docs/steward-acceptance.md
git commit -m "docs: the spec matches the code again"
```

---

## Self-Review

**Spec coverage.** §2 credential → Tasks 1, 2, 4. §3 session → Tasks 2, 5. §4 surfaces → Tasks 5, 7, 8. §5 hub → Task 6. §6 shelf management, pins, and the all-pinned refusal → Task 3, surfaced in 7. §7 settings → Task 8. §8 schema → Task 1. §9 files → the file table. §10 testing → distributed, with the non-UTC clock and the `*sql.Rows` rule in Global Constraints. §11 amendments → Task 9. §12 out of scope → nothing to build.

**Type consistency.** `boulevard.StewardSession` and `StewardSessionTTL` are defined in Task 2 step 1 and used in Tasks 2 and 5. `stewardData` is defined in Task 5 and used in 6, 7, 8. `ErrAllPinned`, `ErrNotShelved` and `ErrPinLimit` are defined in Task 3 step 1 and used in 3, 7 and the CLI. `PinItem` takes `maxPins int` in Task 3 and Task 7 passes 3. `stewardPath(lib)` is defined in Task 5 and used throughout.

**Two things verified against the repo rather than assumed**, because six of this project's plan defects have been exactly this: `boulevard.RandomBase32(r io.Reader, nBytes int)` and `EntropyBytes = 16` exist as written; `queueLibrary(db, slug)` in `cmd/boulevard/queue.go` returns `(*store.Store, boulevard.Library, int)` and is reusable by `steward-key`; `pageTemplates` in `server.go` is the slice new pages must join; `render`'s footer stamp is a type assertion on `pageData` that Task 5 widens to a switch.

**The test helpers were checked rather than assumed**, since six of this project's plan defects have been exactly that mistake. In `internal/web`:

| Helper | Exists? | |
|---|---|---|
| `testStore(t) *store.Store` | yes | `shelf_test.go:18` |
| `addLibrary(t, st, slug) boulevard.Library` | yes | `shelf_test.go:28` |
| `get(t, h http.Handler, path string) *httptest.ResponseRecorder` | yes | `shelf_test.go:45` |
| `postForm` / `getWithCookie` / `cookieNamed` | **no** | must be written |
| `withSession(t, st, lib, req, now)` | yes, but **wrong for this** | `leave_test.go:18` |

`withSession` attaches a **presence** session to a request — it mints a `boulevard.Session` against a token. A steward session is a different table, a different cookie, and no token. Do not reach for it; it will compile against nothing useful here.

Task 5 therefore writes three new helpers in `steward_test.go`, and they are genuinely new rather than duplicates: `cookieNamed(rec, name)`, `postForm(t, h, path, form, c)` and `getWithCookie(t, h, path, c)`. Model them on `get` and `postLeave`'s construction, but do not extend `postLeave` — it is leave-specific and takes a `now` the steward routes have no use for.
