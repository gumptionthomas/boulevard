package store

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeLibrary(t *testing.T) boulevard.Library {
	t.Helper()
	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return boulevard.Library{
		ID:            id,
		Slug:          "fairview",
		Name:          "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       "https://boulevard.example.org",
	}
}

func TestLibraryRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	got, err := s.LibraryBySlug(ctx, "fairview")
	if err != nil {
		t.Fatalf("LibraryBySlug: %v", err)
	}
	if got != lib {
		t.Errorf("round trip = %+v, want %+v", got, lib)
	}
}

func TestLibraryBySlugMissingReturnsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	_, err := s.LibraryBySlug(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateLibraryChangesFieldsButNotID(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	lib.Name = "Renamed"
	lib.BaseURL = "https://new.example.org"
	if err := s.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	got, err := s.LibraryBySlug(ctx, "fairview")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.BaseURL != "https://new.example.org" {
		t.Errorf("update did not persist: %+v", got)
	}
	if got.ID != lib.ID {
		t.Errorf("ID changed from %q to %q; IDs must be stable", lib.ID, got.ID)
	}
}

func makeTokens(t *testing.T, libID boulevard.LibraryID) []boulevard.Token {
	t.Helper()
	periods := tokens.Periods(boulevard.NewDate(2026, time.August, 14), tokens.PeriodCount)
	out := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, boulevard.Token{
			ID: id, LibraryID: libID, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	return out
}

func TestTokenRoundTripIsOrderedByPeriod(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	want := makeTokens(t, lib.ID)
	if err := s.InsertTokens(ctx, lib.ID, want); err != nil {
		t.Fatalf("InsertTokens: %v", err)
	}
	got, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].PeriodIndex != i+1 {
			t.Errorf("slot %d has PeriodIndex %d; results must be ordered", i, got[i].PeriodIndex)
		}
		if got[i].Secret != want[i].Secret {
			t.Errorf("period %d secret = %q, want %q", i+1, got[i].Secret, want[i].Secret)
		}
		if !got[i].ValidFrom.Equal(want[i].ValidFrom) || !got[i].ValidUntil.Equal(want[i].ValidUntil) {
			t.Errorf("period %d dates = %v..%v, want %v..%v",
				i+1, got[i].ValidFrom, got[i].ValidUntil, want[i].ValidFrom, want[i].ValidUntil)
		}
		if got[i].State != boulevard.TokenPending {
			t.Errorf("period %d state = %q, want pending", i+1, got[i].State)
		}
		if got[i].FirstSeenAt != nil {
			t.Errorf("period %d FirstSeenAt = %v, want nil", i+1, got[i].FirstSeenAt)
		}
	}
}

func TestSecretsAreUniqueHostWide(t *testing.T) {
	// DESIGN.md §10: token secrets are unique across the host, not per library,
	// so a scanned token identifies its library unambiguously.
	ctx, s := context.Background(), openTemp(t)
	a, b := makeLibrary(t), makeLibrary(t)
	b.Slug = "other"
	bID, _ := boulevard.NewLibraryID(rand.Reader)
	b.ID = bID
	if err := s.CreateLibrary(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLibrary(ctx, b); err != nil {
		t.Fatal(err)
	}

	toks := makeTokens(t, a.ID)
	if err := s.InsertTokens(ctx, a.ID, toks); err != nil {
		t.Fatal(err)
	}

	clash := makeTokens(t, b.ID)
	clash[0].Secret = toks[0].Secret // same secret, different library
	if err := s.InsertTokens(ctx, b.ID, clash); err == nil {
		t.Error("inserting a duplicate secret across libraries succeeded, want a uniqueness error")
	}
}

func TestInsertTokensIsAtomic(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	toks := makeTokens(t, lib.ID)
	toks[11].Secret = toks[0].Secret // guaranteed constraint violation on the last row
	if err := s.InsertTokens(ctx, lib.ID, toks); err == nil {
		t.Fatal("want error from duplicate secret")
	}
	got, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("found %d tokens after a failed insert; the batch must roll back entirely", len(got))
	}
}

func TestWALModeIsEnabled(t *testing.T) {
	s := openTemp(t)
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q (DESIGN.md §2)", mode, "wal")
	}
}
