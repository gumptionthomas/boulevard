package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/store"
)

func TestBookletDownloadServesAPDF(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/booklet.pdf", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	if got := rec.Body.Bytes(); len(got) < 4 || string(got[:4]) != "%PDF" {
		t.Error("the body is not a PDF")
	}
}

// The round trip, over HTTP: the export is only "a file copy, not a format"
// if a plain store.Open reads back what the desk sent.
func TestExportDownloadOpensAsADatabase(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/export.db", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	path := filepath.Join(t.TempDir(), "downloaded.db")
	if err := os.WriteFile(path, rec.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dst, err := store.Open(path)
	if err != nil {
		t.Fatalf("the downloaded file does not open as a database: %v", err)
	}
	t.Cleanup(func() { dst.Close() })

	got, err := dst.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatalf("the download has no library: %v", err)
	}
	if got.Name != lib.Name {
		t.Errorf("library name = %q, want %q", got.Name, lib.Name)
	}
}

func TestDownloadPagesCarryThePlaintextWarning(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()
	c := loginAsSteward(t, h, lib, key)

	for _, path := range []string{"/steward/tokens", "/steward/export"} {
		rec := getWithCookie(t, h, "/b/"+lib.Slug+path, c)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not encrypted") {
			t.Errorf("%s does not warn that the connection is not encrypted", path)
		}
	}
}

func TestDownloadsRequireASession(t *testing.T) {
	st, lib, _ := stewardServer(t)
	seedWebTokens(t, st, lib)
	h := New(st, time.Now).Handler()

	for _, path := range []string{"/steward/booklet.pdf", "/steward/export", "/steward/export.db"} {
		if rec := get(t, h, "/b/"+lib.Slug+path); rec.Code != http.StatusSeeOther {
			t.Errorf("%s without a session = %d, want 303", path, rec.Code)
		}
	}

	// And nothing at all before a key exists.
	bare := testStore(t)
	other := addLibrary(t, bare, "unarmed")
	bareH := New(bare, time.Now).Handler()
	for _, path := range []string{"/steward/booklet.pdf", "/steward/export", "/steward/export.db"} {
		if rec := get(t, bareH, "/b/"+other.Slug+path); rec.Code != http.StatusNotFound {
			t.Errorf("%s with no steward key = %d, want 404", path, rec.Code)
		}
	}
}
