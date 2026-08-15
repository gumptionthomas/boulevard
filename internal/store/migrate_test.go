package store

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A database created before this milestone has libraries/tokens/sessions but
// none of the §3 settings columns. Opening it must add them rather than
// silently skipping the table, which is what CREATE TABLE IF NOT EXISTS did.
func TestMigrateAddsSettingsToAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	old, err := openRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(migrations[0].stmts); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-migration database: %v", err)
	}
	defer s.Close()

	for _, col := range []string{"slots", "max_age_days", "default_copies", "approval_required", "steward_contact"} {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('libraries') WHERE name = ?`, col).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("column %q missing after migration", col)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		s.Close()
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != len(migrations) {
		t.Errorf("applied %d migrations, want %d — each must run exactly once", applied, len(migrations))
	}
}

// TestMigrateRefusesANewerSchema covers the downgrade: the binary is a
// single file a steward downloads, so running last month's build against
// this month's database is plausible. Without this, migrate skips every
// recorded migration and carries on against a schema it has never seen.
func TestMigrateRefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	future := migrations[len(migrations)-1].version + 1
	if _, err := s.db.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`, future); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = Open(path)
	if err == nil {
		t.Fatal("opening a database from a newer build succeeded, want a refusal")
	}
	for _, want := range []string{strconv.Itoa(future), strconv.Itoa(migrations[len(migrations)-1].version)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; both numbers are what make it actionable", err, want)
		}
	}
}

// The same version is not newer, so a build that matches its database opens
// normally — including the very common case of no migrations left to run.
func TestMigrateAcceptsItsOwnSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		s.Close()
	}
}

func TestMigrateDefaultsMatchDesign(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryBySlug(ctx, lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	// DESIGN.md §3.
	if got.Slots != 12 {
		t.Errorf("Slots = %d, want 12", got.Slots)
	}
	if got.MaxAgeDays != 90 {
		t.Errorf("MaxAgeDays = %d, want 90", got.MaxAgeDays)
	}
	if got.DefaultCopies != 3 {
		t.Errorf("DefaultCopies = %d, want 3", got.DefaultCopies)
	}
	if !got.ApprovalRequired {
		t.Error("ApprovalRequired = false, want true — §5 says approval is default on")
	}
}

func TestLibrarySettingsRoundTrip(t *testing.T) {
	ctx, s := context.Background(), openTemp(t)
	lib := makeLibrary(t)
	if err := s.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	lib.Slots = 6
	lib.MaxAgeDays = 30
	lib.DefaultCopies = 1
	lib.ApprovalRequired = false
	lib.StewardContact = "ruth@example.org"
	if err := s.UpdateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryByID(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != lib {
		t.Errorf("round trip = %+v, want %+v", got, lib)
	}
}
