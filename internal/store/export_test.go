package store

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// The marquee test: an export is only "a file copy, not a format" if a
// plain Open can read it back with no translation.
func TestExportRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)

	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if err := s.RecordScan(ctx, lib.ID, toks[0], now); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	shelved := insertExportItem(t, s, lib, "on the shelf", now)
	if _, err := s.ApproveItem(ctx, lib.ID, shelved, now); err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	waiting := insertExportItem(t, s, lib, "still waiting", now)

	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := s.CopyLibraryTo(ctx, lib.ID, out); err != nil {
		t.Fatalf("CopyLibraryTo: %v", err)
	}

	dst, err := Open(out)
	if err != nil {
		t.Fatalf("Open the export: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	got, err := dst.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatalf("LibraryBySlug on the export: %v", err)
	}
	if got.Name != lib.Name || got.BaseURL != lib.BaseURL {
		t.Errorf("library = %q/%q, want %q/%q", got.Name, got.BaseURL, lib.Name, lib.BaseURL)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat the export: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("export mode = %04o, want 0600 — it holds every card's secret", perm)
	}

	gotToks, err := dst.TokensForLibrary(ctx, got.ID)
	if err != nil {
		t.Fatalf("TokensForLibrary on the export: %v", err)
	}
	if len(gotToks) != len(toks) {
		t.Errorf("%d tokens in the export, want %d", len(gotToks), len(toks))
	}
	// The copy must be runnable, which means the secrets travel intact.
	if gotToks[0].Secret != toks[0].Secret {
		t.Error("token secrets did not survive the export")
	}
	if gotToks[0].State != boulevard.TokenActive {
		t.Errorf("token state = %q, want the scanned card still active", gotToks[0].State)
	}

	shelf, err := dst.ShelvedItems(ctx, got.ID)
	if err != nil {
		t.Fatalf("ShelvedItems on the export: %v", err)
	}
	if len(shelf) != 1 {
		t.Fatalf("%d shelved items in the export, want 1", len(shelf))
	}
	pending, err := dst.PendingItems(ctx, got.ID)
	if err != nil {
		t.Fatalf("PendingItems on the export: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != waiting {
		t.Errorf("pending items = %v, want just %s", pending, waiting)
	}
}

// Live credentials and the take record must not travel. Copying sessions
// would hand working credentials to whoever holds the file; copying
// session_takes would move exactly the durable "who took what" record that
// table's swept-with-the-session design exists to avoid being.
//
// Each of the three tables is asserted non-empty in the *source* before the
// export runs, and only then asserted empty in the export. Without the
// source-side assertion, a fixture that silently failed to populate one of
// these tables would make the "empty in the export" assertion trivially
// true — passing regardless of whether CopyLibraryTo actually excludes it.
func TestExportLeavesEverySessionTableEmpty(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib, toks := seededLibrary(t, s)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	if err := s.RecordScan(ctx, lib.ID, toks[0], now); err != nil {
		t.Fatalf("RecordScan: %v", err)
	}
	sessID, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	sess := boulevard.Session{
		ID: sessID, LibraryID: lib.ID, TokenID: toks[0].ID,
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// A real take, so session_takes has a row CopyLibraryTo could wrongly
	// carry across. Shelve an item first — TakeItem only works on state
	// 'shelved' — the same CreateItem + ApproveItem shape as the round-trip
	// test above.
	taken := insertExportItem(t, s, lib, "up for grabs", now)
	if _, err := s.ApproveItem(ctx, lib.ID, taken, now); err != nil {
		t.Fatalf("ApproveItem: %v", err)
	}
	if err := s.TakeItem(ctx, lib.ID, sess.ID, taken, now, 3); err != nil {
		t.Fatalf("TakeItem: %v", err)
	}

	if err := s.SetStewardKeyHash(ctx, lib.ID, "deadbeef"); err != nil {
		t.Fatalf("SetStewardKeyHash: %v", err)
	}
	stewardID, err := boulevard.NewStewardSessionID(rand.Reader)
	if err != nil {
		t.Fatalf("NewStewardSessionID: %v", err)
	}
	stewardSess := boulevard.StewardSession{
		ID: stewardID, LibraryID: lib.ID,
		CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if err := s.CreateStewardSession(ctx, stewardSess); err != nil {
		t.Fatalf("CreateStewardSession: %v", err)
	}

	// Sanity check the fixture itself: all three tables must actually hold
	// a row in the source, or the "empty in the export" assertion below
	// proves nothing.
	for _, table := range []string{"sessions", "steward_sessions", "session_takes"} {
		if n := countRows(t, s, table); n == 0 {
			t.Fatalf("source %s has 0 rows before export; fixture did not create one", table)
		}
	}

	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := s.CopyLibraryTo(ctx, lib.ID, out); err != nil {
		t.Fatalf("CopyLibraryTo: %v", err)
	}
	dst, err := Open(out)
	if err != nil {
		t.Fatalf("Open the export: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	for _, table := range []string{"sessions", "steward_sessions", "session_takes"} {
		if n := countRows(t, dst, table); n != 0 {
			t.Errorf("%s has %d rows in the export, want 0", table, n)
		}
	}

	// The steward key does travel: §10's story is a host handing out twelve
	// files, and each goes to the steward whose key that hash already is.
	set, err := dst.StewardKeyIsSet(ctx, lib.ID)
	if err != nil {
		t.Fatalf("StewardKeyIsSet: %v", err)
	}
	if !set {
		t.Error("steward key hash did not travel; the recipient is locked out of their own box")
	}
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func insertExportItem(t *testing.T, s *Store, lib boulevard.Library, note string, left time.Time) string {
	t.Helper()
	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		t.Fatal(err)
	}
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemText,
		Payload: note, Note: note, State: boulevard.ItemPending, LeftAt: left,
	}
	if err := s.CreateItem(context.Background(), lib.ID, it); err != nil {
		t.Fatal(err)
	}
	return id
}
