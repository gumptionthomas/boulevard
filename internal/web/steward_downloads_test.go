package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/booklet"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
	"github.com/gumptionthomas/boulevard/internal/version"
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

// A library holding fewer than twelve tokens is what a half-applied
// rotation or a deleted row would leave behind. BuildPlan refuses anything
// that is not exactly twelve, and the handler must turn that into a clean
// 409 with a message a steward can act on — never a broken or partial PDF.
func TestBookletDownloadRefusesAnIncompleteBooklet(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedPartialWebTokens(t, st, lib, 5)
	h := New(st, func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}).Handler()

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/booklet.pdf", loginAsSteward(t, h, lib, key))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), bookletTokensMsg) {
		t.Errorf("body does not carry %q:\n%s", bookletTokensMsg, rec.Body.String())
	}
}

// After a rotation the library holds thirteen or more tokens (the surviving
// active card plus the fresh twelve), and BuildPlan accepts only an exact
// twelve — so the handler must pick precisely the newest booklet, not the
// active leftover mixed in with some of the new ones.
//
// Comparing PDF bytes is normally too brittle for a test to lean on, but
// here it is the strongest assertion available without changing production
// code: the test independently assembles what it believes is the correct
// twelve-token set (every token at or above the newest booklet's start,
// found by inspecting the tokens actually in the store rather than by
// re-implementing the handler's arithmetic) and renders it through the same
// exported booklet.BuildPlan/Renderer the handler calls, with the same
// injected clock. A byte-for-byte match proves the handler selected exactly
// that set of secrets and dates — a wrong `start` would select a different
// set of tokens, which renders to different bytes (different QR payloads,
// different card text), so this is a real assertion, not a status-code
// proxy for one.
func TestBookletDownloadAfterRotationSelectsTheNewestTwelve(t *testing.T) {
	st, lib, key := stewardServer(t)
	seedWebTokens(t, st, lib)
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	h := New(st, func() time.Time { return now }).Handler()
	c := loginAsSteward(t, h, lib, key)

	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/1/force-activate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup force-activate: status = %d", rec.Code)
	}
	if rec := postForm(t, h, "/b/"+lib.Slug+"/steward/tokens/rotate", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup rotate: status = %d", rec.Code)
	}

	all, err := st.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != tokens.PeriodCount+1 {
		t.Fatalf("%d tokens after rotate, want %d (the kept active card plus a fresh twelve)", len(all), tokens.PeriodCount+1)
	}

	// The rotation just minted periods 13..24 (see RotateFromTheDeskKeepsTheActiveCard
	// for the same fixture); the surviving card is period 1. This is domain
	// knowledge about what the setup above did, not a copy of the handler's
	// selection formula, so it does not risk making the test vacuously agree
	// with a broken implementation.
	newest := make([]boulevard.Token, 0, tokens.PeriodCount)
	for _, tok := range all {
		if tok.PeriodIndex >= 13 {
			newest = append(newest, tok)
		}
	}
	if len(newest) != tokens.PeriodCount {
		t.Fatalf("%d tokens at period >= 13, want %d", len(newest), tokens.PeriodCount)
	}

	wantPlan, err := booklet.BuildPlan(booklet.Input{
		Library:   lib,
		Tokens:    newest,
		SourceURL: version.RepoURL,
		BuildLine: "boulevard " + version.Version + " (" + version.Commit + ")",
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := booklet.Renderer{CreationDate: now}.Render(wantPlan)
	if err != nil {
		t.Fatal(err)
	}

	rec := getWithCookie(t, h, "/b/"+lib.Slug+"/steward/booklet.pdf", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 -- body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); len(got) < 4 || string(got[:4]) != "%PDF" {
		t.Fatal("the body is not a PDF")
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Error("the downloaded booklet does not match the newest-twelve booklet built independently from the same tokens -- the handler selected the wrong cards")
	}
}

// seedPartialWebTokens inserts fewer than a full booklet's twelve tokens,
// standing in for a rotation that was interrupted partway or a row deleted
// out from under the library.
func seedPartialWebTokens(t *testing.T, st *store.Store, lib boulevard.Library, n int) []boulevard.Token {
	t.Helper()
	out := make([]boulevard.Token, 0, n)
	for _, p := range tokens.Periods(boulevard.NewDate(2026, time.August, 14), n) {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, boulevard.Token{
			ID: id, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	if err := st.InsertTokens(context.Background(), lib.ID, out); err != nil {
		t.Fatal(err)
	}
	return out
}
