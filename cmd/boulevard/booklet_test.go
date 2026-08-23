package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
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

// A library row with no tokens is what an interrupted first run leaves
// behind: CreateLibrary commits on its own, InsertTokens is a second
// transaction. Before this healed itself, every later run took the reprint
// branch, found nothing, and died in BuildPlan forever.
func TestResolveLibraryHealsALibraryWithNoTokens(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	orphan := boulevard.Library{
		ID: id, Slug: "fairview", Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview", BaseURL: "https://boulevard.example.org",
	}
	if err := s.CreateLibrary(ctx, orphan); err != nil {
		t.Fatal(err) // the library commits; the tokens never arrive
	}

	opts := bookletOpts{
		name: orphan.Name, location: orphan.LocationLabel,
		baseURL: orphan.BaseURL, slug: orphan.Slug,
		installDate: boulevard.DateFromTime(mustTime(t, "2026-08-14")),
	}
	lib, minted, err := resolveLibrary(ctx, s, opts, rand.Reader, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveLibrary on a token-less library: %v", err)
	}
	if lib.ID != orphan.ID {
		t.Errorf("library ID changed from %q to %q; healing must not replace the library", orphan.ID, lib.ID)
	}
	if !minted {
		t.Error("healing minted twelve new secrets, so it must not report itself as a reprint")
	}
	healed, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(healed) != 12 {
		t.Fatalf("got %d tokens after healing, want 12", len(healed))
	}

	// And the run after the heal is an ordinary reprint.
	_, minted2, err := resolveLibrary(ctx, s, opts, rand.Reader, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if minted2 {
		t.Error("the run after a heal must reprint, not mint again")
	}
	again, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range healed {
		if healed[i].Secret != again[i].Secret {
			t.Fatalf("period %d secret changed after healing (%q -> %q)", i+1, healed[i].Secret, again[i].Secret)
		}
	}
}

func TestRunBookletRecoversFromAnInterruptedFirstRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")

	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLibrary(ctx, boulevard.Library{
		ID: id, Slug: "the-fairview-boulevard", Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview", BaseURL: "https://boulevard.example.org",
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	out := filepath.Join(dir, "b.pdf")
	var code int
	stdout := captureStdout(t, func() {
		code = runBooklet([]string{
			"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
			"--base-url", "https://boulevard.example.org",
			"--yes", "--skip-dns", "--db", db, "--out", out,
		})
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; a token-less library must heal, not fail forever", code, exitOK)
	}
	if !strings.Contains(stdout, "no cards") {
		t.Errorf("output does not say the library was missing its cards:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Repaired library the-fairview-boulevard") {
		t.Errorf("summary does not report the repair:\n%s", stdout)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no PDF written: %v", err)
	}
}

// The slug comes from --name and the lookup is by slug, so a one-character
// difference mints a second library with twelve fresh secrets. Say which
// library is being touched, and what will happen to it, before the prompt.
func TestRunBookletNamesTheLibraryItIsAboutToTouch(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")

	run := func(name, out string) string {
		t.Helper()
		var code int
		s := captureStdout(t, func() {
			code = runBooklet([]string{
				"--name", name, "--location", "4th & Fairview",
				"--base-url", "https://boulevard.example.org",
				"--yes", "--skip-dns", "--db", db,
				"--out", filepath.Join(dir, out),
			})
		})
		if code != exitOK {
			t.Fatalf("runBooklet(%q) exit code = %d, want %d\n%s", name, code, exitOK, s)
		}
		return s
	}

	first := run("The Fairview Boulevard", "1.pdf")
	if !strings.Contains(first, `Creating a new library "the-fairview-boulevard"`) {
		t.Errorf("first run does not announce a new library:\n%s", first)
	}
	if !strings.Contains(first, "Created library the-fairview-boulevard") {
		t.Errorf("summary does not name the library it created:\n%s", first)
	}

	second := run("The Fairview Boulevard", "2.pdf")
	if !strings.Contains(second, `Reprinting existing library "the-fairview-boulevard"`) {
		t.Errorf("second run does not announce a reprint:\n%s", second)
	}
	if !strings.Contains(second, "Reprinted library the-fairview-boulevard") {
		t.Errorf("summary does not name the library it reprinted:\n%s", second)
	}

	// The typo: one word dropped from --name.
	typo := run("Fairview Boulevard", "3.pdf")
	if !strings.Contains(typo, `Creating a new library "fairview-boulevard"`) {
		t.Errorf("a typo'd name must announce that it is creating a second library:\n%s", typo)
	}
	if !strings.Contains(typo, "the-fairview-boulevard") {
		t.Errorf("output does not list the library already in the database, so the typo stays invisible:\n%s", typo)
	}
	if !strings.Contains(typo, "Name:      Fairview Boulevard") {
		t.Errorf("output does not echo the name that was typed:\n%s", typo)
	}
}

// This is the tool's most consequential warning. It used to print twice:
// once in the pre-confirmation peek, once inside resolveLibrary.
func TestRunBookletWarnsAboutABaseURLChangeExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	args := func(baseURL, out string) []string {
		return []string{
			"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
			"--base-url", baseURL, "--yes", "--skip-dns",
			"--db", db, "--out", filepath.Join(dir, out),
		}
	}
	if code := runBooklet(args("https://old.example.org", "1.pdf")); code != exitOK {
		t.Fatalf("first run exit code = %d", code)
	}
	out := captureStdout(t, func() {
		if code := runBooklet(args("https://new.example.org", "2.pdf")); code != exitOK {
			t.Fatalf("second run exit code = %d", code)
		}
	})
	if n := strings.Count(out, "Base URL is changing"); n != 1 {
		t.Errorf("the base-URL warning printed %d times, want exactly 1:\n%s", n, out)
	}
}

// A hostname is case-insensitive. Re-running with different casing is not a
// base-URL change and must not warn as though the mounted sign were wrong.
func TestRunBookletDoesNotWarnAboutHostCasing(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	args := func(baseURL, out string) []string {
		return []string{
			"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
			"--base-url", baseURL, "--yes", "--skip-dns",
			"--db", db, "--out", filepath.Join(dir, out),
		}
	}
	if code := runBooklet(args("https://boulevard.example.org", "1.pdf")); code != exitOK {
		t.Fatalf("first run exit code = %d", code)
	}
	out := captureStdout(t, func() {
		if code := runBooklet(args("https://Boulevard.Example.ORG", "2.pdf")); code != exitOK {
			t.Fatalf("second run exit code = %d", code)
		}
	})
	if strings.Contains(out, "Base URL is changing") {
		t.Errorf("host casing alone triggered the base-URL-change warning:\n%s", out)
	}
	if !strings.Contains(out, "Reprinting existing library") {
		t.Errorf("host casing alone stopped the run from reprinting:\n%s", out)
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

// A library name too long for the permanent browse sign is a --name
// problem, not an I/O one, and it must be caught before anything is
// written. Discovering it at render time used to leave a database holding a
// library and twelve tokens behind, with no PDF to show for it.
func TestRunBookletRefusesALongNameBeforeWritingAnything(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	out := filepath.Join(dir, "b.pdf")
	code := runBooklet([]string{
		"--name", "Highland Park Neighborhood Little Free Library",
		"--location", "Highland Park",
		"--base-url", "https://boulevard.example.org",
		"--yes", "--skip-dns", "--db", db, "--out", out,
	})
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d — a name too long for the sign is a usage error, not I/O", code, exitUsage)
	}
	if _, err := os.Stat(db); err == nil {
		t.Error("a refused run left a database behind")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused run left an output file behind")
	}
}

// A base URL too long to print a scannable card is a --base-url problem,
// caught with the other usage errors rather than after the tokens are in
// the database.
func TestRunBookletRefusesALongBaseURLBeforeWritingAnything(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	out := filepath.Join(dir, "b.pdf")
	code := runBooklet([]string{
		"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
		"--base-url", "https://" + strings.Repeat("verylongsubdomain.", 8) + "example.org",
		"--yes", "--skip-dns", "--db", db, "--out", out,
	})
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d — an unprintable base URL is a usage error, not I/O", code, exitUsage)
	}
	if _, err := os.Stat(db); err == nil {
		t.Error("a refused run left a database behind")
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

// Spec §9's flag table documents "--yes / -y". The shorthand was never
// registered, so -y failed with "flag provided but not defined".
func TestRunBookletAcceptsTheYesShorthand(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "booklet.pdf")
	code := runBooklet([]string{
		"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
		"--base-url", "https://boulevard.example.org",
		"-y", "--skip-dns",
		"--db", filepath.Join(dir, "b.db"), "--out", out,
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; -y is documented and must parse", code, exitOK)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no PDF written: %v", err)
	}
}

// The database holds every token secret in plaintext, and a secret is the
// write credential for the shelf. Every card in the PDF carries one in its
// QR. On a shared host, 0644 on either file hands any local account
// leave/take rights without ever standing at the box.
func TestRunBookletWritesOwnerOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	out := filepath.Join(dir, "b.pdf")
	// Start from a world-readable output file so the --force path, where
	// WriteFile keeps the existing mode, is covered too.
	if err := os.WriteFile(out, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := runBooklet([]string{
		"--name", "The Fairview Boulevard", "--location", "4th & Fairview",
		"--base-url", "https://boulevard.example.org",
		"--yes", "--skip-dns", "--force", "--db", db, "--out", out,
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	for _, path := range []string{db, out} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %#o, want %#o; it carries token secrets", filepath.Base(path), got, 0o600)
		}
	}
}

// --rotate mints a new booklet and leaves the card in the door alone.
func TestBookletRotateKeepsTheActiveCard(t *testing.T) {
	ctx := context.Background()
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	seedTokens(t, s, lib)
	if err := s.ForceActivateToken(ctx, lib.ID, 1, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatal(err)
	}
	before, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var activeSecret string
	for _, tok := range before {
		if tok.State == boulevard.TokenActive {
			activeSecret = tok.Secret
		}
	}

	out := filepath.Join(t.TempDir(), "booklet.pdf")
	if code := runBooklet([]string{
		"--db", path, "--slug", "fairview", "--rotate", "--out", out,
		"--yes", "--skip-dns",
	}); code != exitOK {
		t.Fatalf("booklet --rotate exit = %d, want %d", code, exitOK)
	}

	after, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != tokens.PeriodCount+1 {
		t.Fatalf("%d tokens after rotate, want %d", len(after), tokens.PeriodCount+1)
	}
	kept, fresh := false, 0
	for _, tok := range after {
		if tok.Secret == activeSecret && tok.State == boulevard.TokenActive {
			kept = true
		}
		if tok.PeriodIndex >= 13 && tok.PeriodIndex <= 24 {
			fresh++
		}
	}
	if !kept {
		t.Error("the card in the door did not survive --rotate")
	}
	if fresh != tokens.PeriodCount {
		t.Errorf("%d cards at 13..24, want %d", fresh, tokens.PeriodCount)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no PDF written: %v", err)
	}
}

// Regression: after a rotation, a library holds thirteen-plus tokens, and
// the plain reprint path (no --rotate) used to pass every one of them
// straight into booklet.BuildPlan, which refuses anything that is not
// exactly twelve — so reprinting broke the moment a steward rotated even
// once. This is the one command a steward reaches for to replace a lost
// booklet, so it breaking is the worst place for this bug to live. Fixed
// by having runBooklet select tokens.NewestBooklet(toks) before building
// the plan, the same selection --rotate and the desk's booklet download
// already used. This test would have failed against the unfixed code with
// "booklet needs exactly 12 tokens, got 13".
func TestRunBookletReprintsAfterARotation(t *testing.T) {
	ctx := context.Background()
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	seedTokens(t, s, lib)
	if err := s.ForceActivateToken(ctx, lib.ID, 1, boulevard.NewDate(2026, time.August, 20)); err != nil {
		t.Fatal(err)
	}

	rotateOut := filepath.Join(t.TempDir(), "rotated.pdf")
	if code := runBooklet([]string{
		"--db", path, "--slug", "fairview", "--rotate", "--out", rotateOut,
		"--yes", "--skip-dns",
	}); code != exitOK {
		t.Fatalf("booklet --rotate exit = %d, want %d", code, exitOK)
	}

	all, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != tokens.PeriodCount+1 {
		t.Fatalf("test setup: %d tokens after rotate, want %d", len(all), tokens.PeriodCount+1)
	}

	reprintOut := filepath.Join(t.TempDir(), "reprinted.pdf")
	out := captureStdout(t, func() {
		code := runBooklet([]string{
			"--name", lib.Name, "--location", lib.LocationLabel,
			"--base-url", lib.BaseURL, "--slug", "fairview",
			"--yes", "--skip-dns",
			"--db", path, "--out", reprintOut,
		})
		if code != exitOK {
			t.Fatalf("plain reprint after a rotation: exit = %d, want %d", code, exitOK)
		}
	})

	data, err := os.ReadFile(reprintOut)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
	// The success line names the card count it wrote; twelve confirms the
	// newest booklet was selected rather than all thirteen-plus tokens.
	if !strings.Contains(out, "(12 cards)") {
		t.Errorf("reprint did not report 12 cards:\n%s", out)
	}
}

func TestBookletRotateRefusesWithoutAnExistingLibrary(t *testing.T) {
	path, _, _ := cliStore(t)
	out := filepath.Join(t.TempDir(), "booklet.pdf")
	code := runBooklet([]string{
		"--db", path, "--slug", "nowhere", "--rotate", "--out", out,
		"--yes", "--skip-dns",
	})
	if code == exitOK {
		t.Error("--rotate created a library instead of refusing")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("--rotate wrote a PDF for a library that does not exist")
	}
}

// The controller ruling for --rotate: it takes name, location and base URL
// from the stored library rather than from flags, and refuses outright if
// any of the three is passed rather than silently ignoring it — silently
// ignoring --base-url would let a steward believe they had just corrected a
// typo on the one artifact (the mounted sign) that a rerun cannot fix.
func TestBookletRotateRefusesNameLocationOrBaseURL(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])

	cases := []struct {
		flag, value, wantWord string
	}{
		{"--name", "New Name", "name"},
		{"--location", "New Location", "location"},
		{"--base-url", "https://new.example.org", "base URL"},
	}
	for _, tc := range cases {
		t.Run(tc.wantWord, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "booklet.pdf")
			var code int
			stderr := captureStderr(t, func() {
				code = runBooklet([]string{
					"--db", path, "--slug", "fairview", "--rotate",
					tc.flag, tc.value, "--out", out,
					"--yes", "--skip-dns",
				})
			})
			if code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
			want := "it cannot change its " + tc.wantWord + "."
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, want)
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("a refused --rotate wrote a PDF")
			}
		})
	}
}

// The --slug guard was added after review flagged it as an untested
// branch: without it, --rotate with no --slug still refuses safely (the
// empty slug simply resolves to no library), but the message read
// `no library "" to rotate`, which names nothing useful to the steward.
// This states directly what --rotate needs.
func TestBookletRotateRequiresSlug(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	seedTokens(t, s, libs[0])

	out := filepath.Join(t.TempDir(), "booklet.pdf")
	var code int
	stderr := captureStderr(t, func() {
		code = runBooklet([]string{
			"--db", path, "--rotate", "--out", out,
			"--yes", "--skip-dns",
		})
	})
	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--rotate requires --slug") {
		t.Errorf("stderr = %q, want it to name the missing --slug", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("--rotate with no --slug wrote a PDF")
	}
}

// The warning printed before the rotate prompt has to name the cost in the
// case that reads as harmless: with nothing ever scanned there is no card
// in the door to keep, so all twelve printed cards become waste paper and
// the box cannot be written to until a new one is carried to it.
func TestRotateWarningNamesTheCostWhenNothingIsInTheDoor(t *testing.T) {
	var pending []boulevard.Token
	for i := 1; i <= tokens.PeriodCount; i++ {
		pending = append(pending, boulevard.Token{PeriodIndex: i, State: boulevard.TokenPending})
	}

	var dark strings.Builder
	printRotateWarning(&dark, "fairview", pending)
	for _, want := range []string{"all 12 cards are replaced", "left or taken"} {
		if !strings.Contains(dark.String(), want) {
			t.Errorf("warning does not say %q:\n%s", want, dark.String())
		}
	}

	pending[0].State = boulevard.TokenActive
	var lit strings.Builder
	printRotateWarning(&lit, "fairview", pending)
	if !strings.Contains(lit.String(), "keeps working") {
		t.Errorf("warning does not say the card in the door survives:\n%s", lit.String())
	}
	if strings.Contains(lit.String(), "left or taken") {
		t.Errorf("warning claims the box goes dark while a card is in the door:\n%s", lit.String())
	}
}

// captureStdout collects what runBooklet prints. The command writes to
// os.Stdout directly — what a steward sees is part of its contract, so the
// tests read the same stream a steward does.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, r)
		done <- b.String()
	}()
	defer func() {
		os.Stdout = saved
		r.Close()
	}()
	fn()
	w.Close()
	return <-done
}

// captureStderr collects what a command prints to os.Stderr, modelled
// directly on captureStdout above: same signature, same restore-on-return
// discipline. Needed because unlike runBooklet's progress output, an error
// refusal is written where a steward's terminal (not a redirected stdout
// file) shows it — see shed.go's shedDecide.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, r)
		done <- b.String()
	}()
	defer func() {
		os.Stderr = saved
		r.Close()
	}()
	fn()
	w.Close()
	return <-done
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
