package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

func TestConfirmAcceptsYes(t *testing.T) {
	var out bytes.Buffer
	if !confirm(strings.NewReader("y\n"), &out, "Print?") {
		t.Error("confirm(\"y\") = false, want true")
	}
	if !strings.Contains(out.String(), "Print?") {
		t.Error("prompt was not shown to the user")
	}
}

func TestConfirmDefaultsToNo(t *testing.T) {
	// The browse sign is meant to be permanent. A bare Enter must not print.
	for _, in := range []string{"\n", "", "n\n", "nope\n"} {
		var out bytes.Buffer
		if confirm(strings.NewReader(in), &out, "Print?") {
			t.Errorf("confirm(%q) = true, want false", in)
		}
	}
}

func TestResolveLibraryCreatesThenReusesTokens(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "b.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	opts := bookletOpts{
		name: "The Fairview Boulevard", location: "4th & Fairview",
		baseURL: "https://boulevard.example.org", slug: "fairview",
		installDate: boulevard.DateFromTime(mustTime(t, "2026-08-14")),
	}

	lib, created, err := resolveLibrary(ctx, s, opts, rand.Reader, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveLibrary: %v", err)
	}
	if !created {
		t.Error("first call should report the library as newly created")
	}
	first, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 12 {
		t.Fatalf("got %d tokens, want 12", len(first))
	}

	lib2, created2, err := resolveLibrary(ctx, s, opts, rand.Reader, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("second resolveLibrary: %v", err)
	}
	if created2 {
		t.Error("second call should reuse the existing library, not create one")
	}
	if lib2.ID != lib.ID {
		t.Errorf("library ID changed from %q to %q", lib.ID, lib2.ID)
	}
	second, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].Secret != second[i].Secret {
			t.Fatalf("period %d secret changed on reprint (%q -> %q); reprinting must reproduce identical cards",
				i+1, first[i].Secret, second[i].Secret)
		}
	}
}

func TestResolveLibraryWarnsOnBaseURLChangeButProceeds(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	opts := bookletOpts{
		name: "Fairview", location: "4th", slug: "fairview",
		baseURL:     "https://old.example.org",
		installDate: boulevard.DateFromTime(mustTime(t, "2026-08-14")),
	}
	if _, _, err := resolveLibrary(ctx, s, opts, rand.Reader, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var warn bytes.Buffer
	opts.baseURL = "https://new.example.org"
	lib, _, err := resolveLibrary(ctx, s, opts, rand.Reader, &warn)
	if err != nil {
		t.Fatalf("changing base URL should proceed, got %v", err)
	}
	if lib.BaseURL != "https://new.example.org" {
		t.Errorf("BaseURL = %q, want the new value", lib.BaseURL)
	}
	if !strings.Contains(strings.ToLower(warn.String()), "browse sign") {
		t.Errorf("expected a loud warning about the permanent browse sign, got %q", warn.String())
	}
}

func TestRunBookletRejectsBadURL(t *testing.T) {
	code := runBooklet([]string{
		"--name", "X", "--location", "Y",
		"--base-url", "https://example.org/has/a/path",
		"--yes", "--skip-dns",
		"--db", filepath.Join(t.TempDir(), "b.db"),
		"--out", filepath.Join(t.TempDir(), "b.pdf"),
	})
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d for a malformed base URL", code, exitUsage)
	}
}

func TestRunBookletRefusesToOverwriteOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "b.pdf")
	if err := os.WriteFile(out, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := runBooklet([]string{
		"--name", "X", "--location", "Y",
		"--base-url", "https://example.org",
		"--yes", "--skip-dns",
		"--db", filepath.Join(dir, "b.db"), "--out", out,
	})
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d when the output file exists", code, exitUsage)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "existing" {
		t.Error("the existing output file was overwritten")
	}
}

func TestRunBookletEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "booklet.pdf")
	code := runBooklet([]string{
		"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
		"--base-url", "https://boulevard.example.org",
		"--yes", "--skip-dns",
		"--db", filepath.Join(dir, "b.db"), "--out", out,
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
