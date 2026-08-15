package store

import (
	"context"
	"path/filepath"
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
