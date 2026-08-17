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
