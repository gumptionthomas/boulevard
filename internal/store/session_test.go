package store

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func makeSession(t *testing.T, lib boulevard.Library, tokenID string, now time.Time) boulevard.Session {
	t.Helper()
	id, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return boulevard.Session{
		ID:        id,
		LibraryID: lib.ID,
		TokenID:   tokenID,
		CreatedAt: now,
		ExpiresAt: now.Add(boulevard.SessionTTL),
	}
}

func TestSessionRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	want := makeSession(t, lib, toks[1].ID, now)

	if err := s.CreateSession(ctx, want); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.SessionByID(ctx, want.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if got.ID != want.ID || got.LibraryID != want.LibraryID || got.TokenID != want.TokenID {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestSessionRecordsWhichTokenMintedIt(t *testing.T) {
	// DESIGN.md §4 requires this: it is what lets a steward revoke a
	// photographed card and drop the sessions it minted along with it.
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Now().UTC()
	sess := makeSession(t, lib, toks[4].ID, now)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionByID(ctx, sess.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != toks[4].ID {
		t.Errorf("TokenID = %q, want %q", got.TokenID, toks[4].ID)
	}
}

func TestSessionExpiredIsTreatedAsAbsent(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	sess := makeSession(t, lib, toks[1].ID, now)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	justInside := now.Add(boulevard.SessionTTL - time.Second)
	if _, err := s.SessionByID(ctx, sess.ID, justInside); err != nil {
		t.Errorf("one second before expiry: %v, want the session", err)
	}

	justAfter := now.Add(boulevard.SessionTTL + time.Second)
	if _, err := s.SessionByID(ctx, sess.ID, justAfter); !errors.Is(err, ErrNotFound) {
		t.Errorf("after expiry err = %v, want ErrNotFound", err)
	}
}

func TestSessionUnknownIsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	if _, err := s.SessionByID(ctx, boulevard.SessionID("NOPE"), time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteSession(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Now().UTC()
	sess := makeSession(t, lib, toks[1].ID, now)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.SessionByID(ctx, sess.ID, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestSessionTTLIs24Hours(t *testing.T) {
	if boulevard.SessionTTL != 24*time.Hour {
		t.Errorf("SessionTTL = %v, want 24h (DESIGN.md §4)", boulevard.SessionTTL)
	}
}

func TestLeavesCounterRoundTrips(t *testing.T) {
	loc := chicago(t)
	st := openTemp(t)
	// seededLibrary, not seedLibrary — sessions.token_id is a foreign key
	// into tokens and the DSN enforces it, so a made-up token id will not
	// insert. See Task 4's takeSetup for the same note.
	lib, toks := seededLibrary(t, st)
	now := time.Date(2026, 8, 15, 20, 25, 0, 0, loc)
	sess := boulevard.Session{
		ID: "S1", LibraryID: lib.ID, TokenID: toks[0].ID,
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
