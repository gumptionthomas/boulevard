package store

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
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

func TestLibrarySlugsListsEveryLibrary(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	got, err := s.LibrarySlugs(ctx)
	if err != nil {
		t.Fatalf("LibrarySlugs on an empty database: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty database listed %v", got)
	}

	for _, slug := range []string{"whittier", "fairview"} {
		lib := makeLibrary(t)
		lib.Slug = slug
		id, err := boulevard.NewLibraryID(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		lib.ID = id
		if err := s.CreateLibrary(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.LibrarySlugs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "fairview" || got[1] != "whittier" {
		t.Errorf("LibrarySlugs = %v, want [fairview whittier] in slug order", got)
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

// Secrets are stored in plaintext (DESIGN.md §4) and a secret is the write
// credential for the shelf, so the file must never be readable by other
// accounts on the host — including a database an older build left at 0644.
func TestOpenRestrictsTheDatabaseToItsOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	assertOwnerOnly(t, path)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertOwnerOnly(t, path)

	// The write-ahead log holds the same secrets as the database proper.
	ctx := context.Background()
	if err := s.CreateLibrary(ctx, makeLibrary(t)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s-wal mode = %#o; the WAL carries the same secrets", path, got)
		}
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != FileMode {
		t.Errorf("%s mode = %#o, want %#o", path, got, FileMode)
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

func TestTokenBySecretResolvesItsLibrary(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	toks := makeTokens(t, lib.ID)
	if err := s.InsertTokens(ctx, lib.ID, toks); err != nil {
		t.Fatal(err)
	}

	got, err := s.TokenBySecret(ctx, toks[3].Secret)
	if err != nil {
		t.Fatalf("TokenBySecret: %v", err)
	}
	if got.ID != toks[3].ID {
		t.Errorf("ID = %q, want %q", got.ID, toks[3].ID)
	}
	if got.LibraryID != lib.ID {
		t.Errorf("LibraryID = %q, want %q — the secret must identify its library", got.LibraryID, lib.ID)
	}
	if got.PeriodIndex != toks[3].PeriodIndex {
		t.Errorf("PeriodIndex = %d, want %d", got.PeriodIndex, toks[3].PeriodIndex)
	}
	if !got.ValidFrom.Equal(toks[3].ValidFrom) || !got.ValidUntil.Equal(toks[3].ValidUntil) {
		t.Errorf("window = %v..%v, want %v..%v", got.ValidFrom, got.ValidUntil, toks[3].ValidFrom, toks[3].ValidUntil)
	}
}

func TestTokenBySecretUnknownIsErrNotFound(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	_, err := s.TokenBySecret(ctx, "QQQQQQQQQQQQQQQQQQQQQQQQQQ")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLibraryByID(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryByID(ctx, lib.ID)
	if err != nil {
		t.Fatalf("LibraryByID: %v", err)
	}
	if got != lib {
		t.Errorf("got %+v, want %+v", got, lib)
	}

	if _, err := s.LibraryByID(ctx, boulevard.LibraryID("NOPE")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v, want ErrNotFound", err)
	}
}

func TestLibraryCount(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	if n, err := s.LibraryCount(ctx); err != nil || n != 0 {
		t.Fatalf("empty count = %d, %v; want 0, nil", n, err)
	}
	a := makeLibrary(t)
	if err := s.CreateLibrary(ctx, a); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.LibraryCount(ctx); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	b := makeLibrary(t)
	b.Slug = "other"
	bID, _ := boulevard.NewLibraryID(rand.Reader)
	b.ID = bID
	if err := s.CreateLibrary(ctx, b); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.LibraryCount(ctx); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func seededLibrary(t *testing.T, s *Store) (boulevard.Library, []boulevard.Token) {
	t.Helper()
	ctx := context.Background()
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	toks := makeTokens(t, lib.ID)
	if err := s.InsertTokens(ctx, lib.ID, toks); err != nil {
		t.Fatal(err)
	}
	return lib, toks
}

func stateOf(t *testing.T, s *Store, lib boulevard.Library, periodIndex int) boulevard.Token {
	t.Helper()
	all, err := s.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range all {
		if tok.PeriodIndex == periodIndex {
			return tok
		}
	}
	t.Fatalf("no token with period %d", periodIndex)
	return boulevard.Token{}
}

func TestRecordScanActivatesAndStampsFirstSeen(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)

	if err := s.RecordScan(ctx, lib.ID, toks[1], now); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	got := stateOf(t, s, lib, 2)
	if got.State != boulevard.TokenActive {
		t.Errorf("state = %q, want active", got.State)
	}
	if got.FirstSeenAt == nil {
		t.Fatal("FirstSeenAt is nil, want it stamped")
	}
	if !got.FirstSeenAt.Equal(now) {
		t.Errorf("FirstSeenAt = %v, want %v", got.FirstSeenAt, now)
	}
}

func TestRecordScanAdvancesActiveAndExpiresPredecessor(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)

	if err := s.RecordScan(ctx, lib.ID, toks[1], now); err != nil {
		t.Fatal(err)
	}
	later := now.AddDate(0, 1, 0)
	if err := s.RecordScan(ctx, lib.ID, toks[2], later); err != nil {
		t.Fatal(err)
	}

	if got := stateOf(t, s, lib, 3); got.State != boulevard.TokenActive {
		t.Errorf("period 3 state = %q, want active", got.State)
	}
	if got := stateOf(t, s, lib, 2); got.State != boulevard.TokenExpired {
		t.Errorf("period 2 state = %q, want expired", got.State)
	}
}

func TestRecordScanOfAnOlderTokenDoesNotRewindActive(t *testing.T) {
	// 3 September, the September card is active. Someone finds the August
	// card — never scanned, because the steward forgot to put it up — and
	// scans it inside its grace window. It must be seen, but must not
	// become the active card. (Spec §7.)
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)

	if err := s.RecordScan(ctx, lib.ID, toks[1], now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordScan(ctx, lib.ID, toks[0], now.Add(time.Hour)); err != nil {
		t.Fatalf("scanning an older token must still succeed: %v", err)
	}

	if got := stateOf(t, s, lib, 2); got.State != boulevard.TokenActive {
		t.Errorf("period 2 state = %q, want it still active", got.State)
	}
	old := stateOf(t, s, lib, 1)
	if old.State == boulevard.TokenActive {
		t.Error("period 1 became active; activation must only move forward")
	}
	if old.FirstSeenAt == nil {
		t.Error("period 1 FirstSeenAt is nil; an older scan must still be recorded")
	}
}

func TestRecordScanDoesNotOverwriteFirstSeen(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	first := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	second := first.Add(48 * time.Hour)

	if err := s.RecordScan(ctx, lib.ID, toks[1], first); err != nil {
		t.Fatal(err)
	}
	seen := stateOf(t, s, lib, 2)
	if err := s.RecordScan(ctx, lib.ID, seen, second); err != nil {
		t.Fatal(err)
	}
	got := stateOf(t, s, lib, 2)
	if !got.FirstSeenAt.Equal(first) {
		t.Errorf("FirstSeenAt = %v, want the original %v — it records the FIRST scan", got.FirstSeenAt, first)
	}
}

func TestRecordScanRejectsAForeignToken(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	other := toks[0]
	other.LibraryID = boulevard.LibraryID("SOMEONEELSE")
	if err := s.RecordScan(ctx, lib.ID, other, time.Now()); err == nil {
		t.Error("recording a token from another library succeeded, want an error")
	}
	_ = lib
}
