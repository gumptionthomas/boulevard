package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gumptionthomas/boulevard/internal/store"
)

// TestExportWritesARunnableFile is the whole point of the command: the
// output is a file boulevard.serve can open unchanged, not a format.
func TestExportWritesARunnableFile(t *testing.T) {
	dbPath, srcStore, libs := cliStore(t, "fairview")
	seedTokens(t, srcStore, libs[0])

	out := filepath.Join(t.TempDir(), "fairview.db")
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out}); code != exitOK {
		t.Fatalf("exit = %d, want %d", code, exitOK)
	}
	s, err := store.Open(out)
	if err != nil {
		t.Fatalf("the export does not open: %v", err)
	}
	defer s.Close()
	if _, err := s.LibraryBySlug(context.Background(), "fairview"); err != nil {
		t.Errorf("the export has no library: %v", err)
	}
}

// TestExportRefusesToOverwriteWithoutForce covers both halves the carried
// finding calls out: refusal without --force, and a genuine single-library
// file with it — CopyLibraryTo does not itself reject an existing target,
// so runExport must remove it first rather than merely permit the append.
func TestExportRefusesToOverwriteWithoutForce(t *testing.T) {
	dbPath, srcStore, libs := cliStore(t, "fairview")
	seedTokens(t, srcStore, libs[0])

	out := filepath.Join(t.TempDir(), "fairview.db")
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out}); code == exitOK {
		t.Error("overwrote an existing file without --force")
	}
	if code := runExport([]string{"--db", dbPath, "--slug", "fairview", "--out", out, "--force"}); code != exitOK {
		t.Errorf("--force did not permit the overwrite, exit = %d", code)
	}

	// --force must remove the existing file before CopyLibraryTo runs, not
	// merely let Open append into it — otherwise this holds two libraries'
	// worth of rows rather than one.
	s, err := store.Open(out)
	if err != nil {
		t.Fatalf("the export does not open: %v", err)
	}
	defer s.Close()
	slugs, err := s.LibrarySlugs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(slugs) != 1 {
		t.Errorf("--force export holds %d libraries, want 1: %v", len(slugs), slugs)
	}
}
