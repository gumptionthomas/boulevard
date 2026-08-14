# Booklet Generator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `boulevard booklet`, a command that turns a library's name, location, and base URL into a SQLite database of twelve time-gated tokens plus a printable US Letter PDF booklet.

**Architecture:** A pure-Go single binary. Domain types live in `internal/boulevard` with a distinct `LibraryID` type that every store method takes explicitly — no ambient current-library value anywhere. The booklet package splits *planning* (pure data describing what goes where) from *rendering* (drawing that plan to PDF), so all layout logic is unit-testable without parsing a PDF.

**Tech Stack:** Go 1.26, `github.com/go-pdf/fpdf` (PDF), `github.com/skip2/go-qrcode` (QR), `modernc.org/sqlite` (pure-Go SQLite driver).

**Spec:** `docs/superpowers/specs/2026-08-14-booklet-generator-design.md`

## Global Constraints

Every task's requirements implicitly include this section.

- **Module path:** `github.com/gumptionthomas/boulevard`
- **Go version floor:** 1.26
- **The SQLite driver MUST be pure Go** (`modernc.org/sqlite`). A cgo driver forfeits static cross-compiled binaries and breaks the "download one file, run it" install promise. This is not negotiable.
- **All dependency licenses must be AGPLv3-compatible** — MIT, BSD, or Apache-2.0 only. No GPLv2-only dependencies.
- **No ambient current-library value.** Every store method takes a `boulevard.LibraryID` parameter explicitly.
- **Token secrets are stored in plaintext.** Never hash them. Reprinting must reproduce identical cards.
- **All PDF dimensions are in PostScript points** (72 pt = 1 in). US Letter is 612 × 792 pt.
- **Randomness and dates are always injected**, never called ambiently, so output is deterministic under test.
- **Card purpose bar copy, verbatim:** `SCAN TO LEAVE OR TAKE`
- **Browse sign copy, verbatim:** `Scan to browse the shelf` and `Anyone, anywhere, anytime. No code needed.`
- **Signage line, verbatim:** `Take something. Leave something. Scan to leave.`
- **Crockford base32 alphabet, verbatim:** `0123456789ABCDEFGHJKMNPQRSTVWXYZ`

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `LICENSE` | Module definition, AGPL-3.0 |
| `internal/version/version.go` | Build metadata injected via ldflags |
| `internal/boulevard/date.go` | `Date` — bare calendar date and its month arithmetic |
| `internal/boulevard/id.go` | Crockford base32 random ID generation |
| `internal/boulevard/library.go` | `LibraryID`, `Library`, `Slugify`, `ValidateBaseURL` |
| `internal/boulevard/token.go` | `Token`, `TokenState` |
| `internal/tokens/secret.go` | Token secret generation |
| `internal/tokens/periods.go` | Calendar period assignment |
| `internal/store/store.go` | SQLite open, pragmas, schema |
| `internal/store/library.go` | Library repository |
| `internal/store/token.go` | Token repository (library-scoped) |
| `internal/booklet/qr.go` | QR encoding to a module bitmap |
| `internal/booklet/geometry.go` | All printed dimensions as named constants |
| `internal/booklet/plan.go` | Pure layout planning |
| `internal/booklet/render.go` | Plan → PDF, shared drawing helpers |
| `internal/booklet/card.go` | Monthly card face |
| `internal/booklet/cover.go` | Cover sheet and browse sign |
| `cmd/boulevard/main.go` | Subcommand dispatch, exit codes |
| `cmd/boulevard/booklet.go` | The `booklet` subcommand |
| `cmd/boulevard/version.go` | The `version` subcommand |

---

## Task 1: Scaffolding, license, and version

**Files:**
- Create: `go.mod`, `LICENSE`, `internal/version/version.go`, `cmd/boulevard/main.go`, `cmd/boulevard/version.go`
- Modify: `DESIGN.md` (the two §13 amendments)
- Test: `internal/version/version_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `version.Version`, `version.Commit`, `version.RepoURL` (all `string`); `version.String() string` returning `"boulevard <Version> (<Commit>)\n<RepoURL>"`

- [ ] **Step 1: Initialize the module and fetch dependencies**

```bash
cd /home/gumptionthomas/Development/boulevard
go mod init github.com/gumptionthomas/boulevard
go get github.com/go-pdf/fpdf
go get github.com/skip2/go-qrcode
go get modernc.org/sqlite
go mod tidy
```

- [ ] **Step 2: Add the AGPL-3.0 license**

Download the canonical text to `LICENSE`:

```bash
curl -fsSL https://www.gnu.org/licenses/agpl-3.0.txt -o LICENSE
head -3 LICENSE   # expect: GNU AFFERO GENERAL PUBLIC LICENSE / Version 3, 19 November 2007
```

If the machine is offline, copy the text from any AGPL-3.0 project rather than paraphrasing it.

- [ ] **Step 3: Write the failing version test**

Create `internal/version/version_test.go`:

```go
package version

import "testing"

func TestStringIncludesAllBuildMetadata(t *testing.T) {
	Version, Commit, RepoURL = "1.2.3", "abc1234", "https://example.org/repo"
	got := String()
	want := "boulevard 1.2.3 (abc1234)\nhttps://example.org/repo"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultsAreNotEmpty(t *testing.T) {
	if defaultVersion == "" || defaultCommit == "" || defaultRepoURL == "" {
		t.Fatal("build metadata defaults must be non-empty so an un-stamped build still identifies itself")
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/version/`
Expected: FAIL — `undefined: Version`

- [ ] **Step 5: Write the version package**

Create `internal/version/version.go`:

```go
// Package version carries build metadata stamped in at link time.
//
// AGPL-3.0 places the source-offer obligation on whoever runs a modified
// build. DESIGN.md §2 answers that with compliance by construction: the
// binary always knows its own version, commit, and repository.
package version

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	defaultRepoURL = "https://github.com/gumptionthomas/boulevard"
)

// Overridden at build time with -ldflags "-X ...".
var (
	Version = defaultVersion
	Commit  = defaultCommit
	RepoURL = defaultRepoURL
)

// String renders the metadata for `boulevard version` and the booklet cover footer.
func String() string {
	return "boulevard " + Version + " (" + Commit + ")\n" + RepoURL
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/version/`
Expected: PASS

- [ ] **Step 7: Write the CLI entry point**

Create `cmd/boulevard/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

// Exit codes, per spec §9.2.
const (
	exitOK       = 0
	exitDeclined = 1
	exitUsage    = 2
	exitIO       = 3
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	switch os.Args[1] {
	case "booklet":
		os.Exit(runBooklet(os.Args[2:]))
	case "version":
		os.Exit(runVersion())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `boulevard — a shelf you have to stand at

Usage:
  boulevard booklet --name NAME --location LABEL --base-url URL [flags]
  boulevard version
`)
}
```

Create `cmd/boulevard/version.go`:

```go
package main

import (
	"fmt"

	"github.com/gumptionthomas/boulevard/internal/version"
)

func runVersion() int {
	fmt.Println(version.String())
	return exitOK
}
```

Create a temporary stub so the package compiles — Task 11 replaces it:

```go
// in cmd/boulevard/booklet.go
package main

func runBooklet(args []string) int {
	panic("not implemented until Task 11")
}
```

- [ ] **Step 8: Verify the binary builds and reports itself**

```bash
go build -o /tmp/boulevard ./cmd/boulevard && /tmp/boulevard version
```

Expected: `boulevard dev (unknown)` then the repo URL.

Now verify ldflags stamping works:

```bash
go build -ldflags "-X github.com/gumptionthomas/boulevard/internal/version.Version=0.1.0 -X github.com/gumptionthomas/boulevard/internal/version.Commit=$(git rev-parse --short HEAD)" -o /tmp/boulevard ./cmd/boulevard && /tmp/boulevard version
```

Expected: `boulevard 0.1.0 (<short sha>)`

- [ ] **Step 9: Apply the two DESIGN.md amendments from spec §13**

Edit `DESIGN.md`. Replace the two bullets under `### The two codes`:

```markdown
- **Browse code** — stable, generated once, printed once, mounted permanently. Encodes `https://host/`. Reads *"Scan to browse the shelf — anyone, anywhere, anytime. No code needed."*
- **Leave/take code** — rotating monthly, swapped by the steward from a printed booklet. Encodes `https://host/s/<token>`. Reads *"Scan to leave or take."*
```

Then replace the signage line:

```markdown
**Signage line:** `Take something. Leave something. Scan to leave.`

The mounted sign carries that line. **The monthly card says "Scan to leave or take"** — §5 gates taking behind a session identically to leaving, and a visitor who reads only "scan to leave" will tap *take* and find the control inert. Each code names its own verb, and the two artifacts deliberately do not resemble each other: browse is mounted, light, landscape, undated; leave/take is inside the door, dark-barred, dated, business-card sized.
```

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum LICENSE internal/version cmd/boulevard DESIGN.md
git commit -m "feat: module scaffolding, AGPL license, version command

Applies the two DESIGN.md amendments from the milestone 0 spec so the
card and the browse sign each name their own verb."
```

---

## Task 2: The Date type

**Files:**
- Create: `internal/boulevard/date.go`
- Test: `internal/boulevard/date_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Date struct { Year int; Month time.Month; Day int }`
  - `func NewDate(y int, m time.Month, d int) Date`
  - `func DateFromTime(t time.Time) Date`
  - `func ParseDate(s string) (Date, error)` — accepts `YYYY-MM-DD`
  - `func (d Date) String() string` — renders `YYYY-MM-DD`
  - `func (d Date) FirstOfMonth() Date`
  - `func (d Date) LastOfMonth() Date`
  - `func (d Date) NextMonth() Date` — first day of the following month
  - `func (d Date) Before(o Date) bool`, `After`, `Equal`
  - `func (d Date) MonthName() string` — e.g. `"August"`

**Why a bare date and not `time.Time`:** period boundaries are calendar facts. Carrying a timezone through them invites exactly the offset and DST bugs that DESIGN.md §4 rejects TOTP-derived codes to avoid.

- [ ] **Step 1: Write the failing tests**

Create `internal/boulevard/date_test.go`:

```go
package boulevard

import (
	"testing"
	"time"
)

func TestLastOfMonth(t *testing.T) {
	tests := []struct {
		name string
		in   Date
		want Date
	}{
		{"31-day month", NewDate(2026, time.August, 14), NewDate(2026, time.August, 31)},
		{"30-day month", NewDate(2026, time.September, 1), NewDate(2026, time.September, 30)},
		{"february common year", NewDate(2027, time.February, 3), NewDate(2027, time.February, 28)},
		{"february leap year", NewDate(2028, time.February, 3), NewDate(2028, time.February, 29)},
		{"already last day", NewDate(2026, time.December, 31), NewDate(2026, time.December, 31)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.LastOfMonth(); !got.Equal(tc.want) {
				t.Errorf("LastOfMonth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextMonthReturnsFirstDay(t *testing.T) {
	tests := []struct {
		name string
		in   Date
		want Date
	}{
		{"mid month", NewDate(2026, time.August, 14), NewDate(2026, time.September, 1)},
		{"from the 31st", NewDate(2026, time.January, 31), NewDate(2026, time.February, 1)},
		{"year rollover", NewDate(2026, time.December, 15), NewDate(2027, time.January, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.NextMonth(); !got.Equal(tc.want) {
				t.Errorf("NextMonth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringAndParseRoundTrip(t *testing.T) {
	d := NewDate(2026, time.August, 14)
	if got := d.String(); got != "2026-08-14" {
		t.Fatalf("String() = %q, want %q", got, "2026-08-14")
	}
	back, err := ParseDate("2026-08-14")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if !back.Equal(d) {
		t.Errorf("round trip = %v, want %v", back, d)
	}
}

func TestParseDateRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "2026-13-01", "not-a-date", "2026/08/14"} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) succeeded, want error", s)
		}
	}
}

func TestOrdering(t *testing.T) {
	early, late := NewDate(2026, time.August, 14), NewDate(2026, time.September, 1)
	if !early.Before(late) {
		t.Error("early.Before(late) = false")
	}
	if !late.After(early) {
		t.Error("late.After(early) = false")
	}
	if early.Before(early) || early.After(early) {
		t.Error("a date must be neither before nor after itself")
	}
}

func TestMonthName(t *testing.T) {
	if got := NewDate(2026, time.August, 14).MonthName(); got != "August" {
		t.Errorf("MonthName() = %q, want %q", got, "August")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/boulevard/`
Expected: FAIL — `undefined: NewDate`

- [ ] **Step 3: Implement the Date type**

Create `internal/boulevard/date.go`:

```go
// Package boulevard holds the domain types. It depends on nothing but the
// standard library.
package boulevard

import (
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// Date is a bare calendar date with no timezone. Period boundaries are
// calendar facts, not instants; see DESIGN.md §4 on why TOTP-style
// time derivation is rejected.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func NewDate(y int, m time.Month, d int) Date { return Date{Year: y, Month: m, Day: d} }

func DateFromTime(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return DateFromTime(t), nil
}

func (d Date) String() string { return d.time().Format(dateLayout) }

// time converts to a UTC instant at midnight. Used only for arithmetic and
// formatting — the zone is never observable outside this file.
func (d Date) time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) FirstOfMonth() Date { return NewDate(d.Year, d.Month, 1) }

// LastOfMonth relies on time.Date normalizing day 0 of the following month
// into the last day of this one, which handles leap years for free.
func (d Date) LastOfMonth() Date {
	return DateFromTime(time.Date(d.Year, d.Month+1, 0, 0, 0, 0, 0, time.UTC))
}

func (d Date) NextMonth() Date {
	return DateFromTime(time.Date(d.Year, d.Month+1, 1, 0, 0, 0, 0, time.UTC))
}

func (d Date) Before(o Date) bool { return d.time().Before(o.time()) }
func (d Date) After(o Date) bool  { return d.time().After(o.time()) }
func (d Date) Equal(o Date) bool  { return d == o }

func (d Date) MonthName() string { return d.Month.String() }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/boulevard/ -v`
Expected: PASS, all subtests

- [ ] **Step 5: Commit**

```bash
git add internal/boulevard/date.go internal/boulevard/date_test.go
git commit -m "feat: bare calendar Date type with month arithmetic"
```

---

## Task 3: Library identity, slugs, and base URL validation

**Files:**
- Create: `internal/boulevard/id.go`, `internal/boulevard/library.go`, `internal/boulevard/token.go`
- Test: `internal/boulevard/id_test.go`, `internal/boulevard/library_test.go`

**Interfaces:**
- Consumes: `Date` (Task 2)
- Produces:
  - `const CrockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"`
  - `func RandomBase32(r io.Reader, nBytes int) (string, error)`
  - `type LibraryID string`
  - `func NewLibraryID(r io.Reader) (LibraryID, error)`
  - `type Library struct { ID LibraryID; Slug, Name, LocationLabel, BaseURL string }`
  - `func Slugify(s string) string`
  - `func ValidateBaseURL(raw string) (string, error)` — returns the normalized URL with no trailing slash
  - `type TokenState string` with `TokenPending`, `TokenActive`, `TokenExpired`, `TokenRevoked`
  - `type Token struct { ID string; LibraryID LibraryID; Secret string; PeriodIndex int; ValidFrom, ValidUntil Date; State TokenState; FirstSeenAt *time.Time }`

`LibraryID` is deliberately **not** the slug — slugs are renameable, IDs must be stable, and DESIGN.md §10 requires host-wide uniqueness so a database file can be copied out and stand alone.

- [ ] **Step 1: Write the failing tests**

Create `internal/boulevard/id_test.go`:

```go
package boulevard

import (
	"bytes"
	"strings"
	"testing"
)

func TestRandomBase32LengthAndAlphabet(t *testing.T) {
	s, err := RandomBase32(bytes.NewReader(make([]byte, 16)), 16)
	if err != nil {
		t.Fatalf("RandomBase32: %v", err)
	}
	if len(s) != 26 {
		t.Errorf("len = %d, want 26 (128 bits at 5 bits per char)", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(CrockfordAlphabet, r) {
			t.Errorf("character %q is not in the Crockford alphabet", r)
		}
	}
}

func TestCrockfordAlphabetExcludesAmbiguousLetters(t *testing.T) {
	if len(CrockfordAlphabet) != 32 {
		t.Fatalf("alphabet has %d characters, want 32", len(CrockfordAlphabet))
	}
	for _, bad := range "ILOU" {
		if strings.ContainsRune(CrockfordAlphabet, bad) {
			t.Errorf("alphabet must exclude %q — DESIGN.md §4 requires no ambiguous characters", bad)
		}
	}
}

func TestRandomBase32IsDeterministicForAGivenReader(t *testing.T) {
	src := []byte("0123456789abcdef")
	a, err := RandomBase32(bytes.NewReader(src), 16)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RandomBase32(bytes.NewReader(src), 16)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("same reader bytes produced %q and %q; generation must be injectable for tests", a, b)
	}
}

func TestRandomBase32FailsOnShortReader(t *testing.T) {
	if _, err := RandomBase32(bytes.NewReader([]byte{1, 2, 3}), 16); err == nil {
		t.Error("want error when the reader cannot supply enough entropy")
	}
}
```

Create `internal/boulevard/library_test.go`:

```go
package boulevard

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Fairview Boulevard", "the-fairview-boulevard"},
		{"4th & Fairview", "4th-fairview"},
		{"  padded  ", "padded"},
		{"Ümlaut Box", "mlaut-box"},
		{"multiple---hyphens", "multiple-hyphens"},
	}
	for _, tc := range tests {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateBaseURLAccepts(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://boulevard.example.org", "https://boulevard.example.org"},
		{"https://boulevard.example.org/", "https://boulevard.example.org"},
		{"http://localhost:8080", "http://localhost:8080"},
	}
	for _, tc := range tests {
		got, err := ValidateBaseURL(tc.in)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateBaseURLRejects(t *testing.T) {
	// Every card and the permanent browse sign encode this host, so anything
	// that would produce a wrong or unstable URL must be refused (spec §9.1).
	for _, in := range []string{
		"",
		"boulevard.example.org",       // no scheme
		"ftp://boulevard.example.org", // wrong scheme
		"https://",                    // no host
		"https://example.org/shelf",   // path
		"https://example.org?a=1",     // query
		"https://example.org#frag",    // fragment
		"https://user:pw@example.org", // credentials
	} {
		if _, err := ValidateBaseURL(in); err == nil {
			t.Errorf("ValidateBaseURL(%q) succeeded, want error", in)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/boulevard/`
Expected: FAIL — `undefined: RandomBase32`, `undefined: Slugify`

- [ ] **Step 3: Implement identity and validation**

Create `internal/boulevard/id.go`:

```go
package boulevard

import (
	"encoding/base32"
	"fmt"
	"io"
)

// CrockfordAlphabet excludes I, L, O and U so a secret read off paper cannot
// be mistyped into a different valid secret (DESIGN.md §4).
const CrockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var crockford = base32.NewEncoding(CrockfordAlphabet).WithPadding(base32.NoPadding)

// RandomBase32 reads nBytes of entropy and encodes it. The reader is a
// parameter so tests can be deterministic; production passes crypto/rand.Reader.
func RandomBase32(r io.Reader, nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read %d bytes of entropy: %w", nBytes, err)
	}
	return crockford.EncodeToString(buf), nil
}
```

Create `internal/boulevard/library.go`:

```go
package boulevard

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// EntropyBytes is 128 bits, per DESIGN.md §4.
const EntropyBytes = 16

// LibraryID is a distinct type so the compiler enforces DESIGN.md §10:
// every repository method takes one, and no ambient "current library"
// exists anywhere in the codebase.
//
// It is deliberately not the slug. Slugs are renameable; an ID must be
// stable and unique across a host so a database file can be copied out
// and stand alone.
type LibraryID string

func NewLibraryID(r io.Reader) (LibraryID, error) {
	s, err := RandomBase32(r, EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new library id: %w", err)
	}
	return LibraryID(s), nil
}

type Library struct {
	ID            LibraryID
	Slug          string
	Name          string
	LocationLabel string
	BaseURL       string
}

// Slugify lowercases, replaces runs of non-alphanumerics with a single
// hyphen, and trims hyphens from the ends.
func Slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidateBaseURL enforces scheme + host and nothing else. Every token card
// and the permanent browse sign encode this value, so a path or query here
// becomes twelve dead cards and a wrong permanent sign (DESIGN.md §8).
func ValidateBaseURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("base URL must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("base URL must include a host")
	}
	if u.User != nil {
		return "", errors.New("base URL must not include credentials")
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", fmt.Errorf("base URL must not include a path, got %q", u.Path)
	}
	if u.RawQuery != "" {
		return "", errors.New("base URL must not include a query string")
	}
	if u.Fragment != "" {
		return "", errors.New("base URL must not include a fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}
```

Create `internal/boulevard/token.go`:

```go
package boulevard

import "time"

type TokenState string

const (
	TokenPending TokenState = "pending"
	TokenActive  TokenState = "active"
	TokenExpired TokenState = "expired"
	TokenRevoked TokenState = "revoked"
)

// Token is one month's leave/take credential.
//
// Secret is stored in plaintext on purpose (DESIGN.md §4): a lost booklet is
// far likelier than server compromise, and reprinting must reproduce
// identical cards. Never hash it.
type Token struct {
	ID          string
	LibraryID   LibraryID
	Secret      string
	PeriodIndex int
	ValidFrom   Date
	ValidUntil  Date
	State       TokenState
	FirstSeenAt *time.Time
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/boulevard/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/boulevard/
git commit -m "feat: LibraryID, slug derivation, and base URL validation"
```

---

## Task 4: Token secrets

**Files:**
- Create: `internal/tokens/secret.go`
- Test: `internal/tokens/secret_test.go`

**Interfaces:**
- Consumes: `boulevard.RandomBase32`, `boulevard.EntropyBytes`
- Produces: `const SecretLen = 26`; `func NewSecret(r io.Reader) (string, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/tokens/secret_test.go`:

```go
package tokens

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func TestNewSecretShape(t *testing.T) {
	s, err := NewSecret(rand.Reader)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	if len(s) != SecretLen {
		t.Errorf("len = %d, want %d", len(s), SecretLen)
	}
	for _, r := range s {
		if !strings.ContainsRune(boulevard.CrockfordAlphabet, r) {
			t.Errorf("character %q is not in the Crockford alphabet", r)
		}
	}
}

func TestNewSecretIsUniqueAcrossManyDraws(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s, err := NewSecret(rand.Reader)
		if err != nil {
			t.Fatalf("NewSecret: %v", err)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q after %d draws", s, i)
		}
		seen[s] = true
	}
}

func TestNewSecretIsDeterministicUnderAFixedReader(t *testing.T) {
	src := []byte("boulevard-fixed!")
	a, _ := NewSecret(bytes.NewReader(src))
	b, _ := NewSecret(bytes.NewReader(src))
	if a != b {
		t.Errorf("got %q and %q; generation must be injectable so golden tests are stable", a, b)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tokens/`
Expected: FAIL — `undefined: NewSecret`

- [ ] **Step 3: Implement secret generation**

Create `internal/tokens/secret.go`:

```go
// Package tokens generates leave/take token secrets and assigns them
// explicit calendar periods.
package tokens

import (
	"fmt"
	"io"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// SecretLen is the encoded length of 128 bits at 5 bits per character.
const SecretLen = 26

func NewSecret(r io.Reader) (string, error) {
	s, err := boulevard.RandomBase32(r, boulevard.EntropyBytes)
	if err != nil {
		return "", fmt.Errorf("new token secret: %w", err)
	}
	return s, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/tokens/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tokens/
git commit -m "feat: token secret generation"
```

---

## Task 5: Calendar periods

**Files:**
- Create: `internal/tokens/periods.go`
- Test: `internal/tokens/periods_test.go`

**Interfaces:**
- Consumes: `boulevard.Date` (Task 2)
- Produces:
  - `const PeriodCount = 12`
  - `type Period struct { Index int; From, Until boulevard.Date }`
  - `func Periods(install boulevard.Date, n int) []Period`

**Rule (DESIGN.md §4):** period 1 runs from the install date to the last day of that calendar month, however short. Periods 2..n are whole calendar months. A short first period is correct — it aligns every later card to a month boundary, which is what a steward can remember.

- [ ] **Step 1: Write the failing tests**

Create `internal/tokens/periods_test.go`:

```go
package tokens

import (
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func d(y int, m time.Month, day int) boulevard.Date { return boulevard.NewDate(y, m, day) }

func TestFirstPeriodRunsToEndOfInstallMonth(t *testing.T) {
	got := Periods(d(2026, time.August, 14), PeriodCount)
	if len(got) != PeriodCount {
		t.Fatalf("len = %d, want %d", len(got), PeriodCount)
	}
	first := got[0]
	if first.Index != 1 {
		t.Errorf("Index = %d, want 1", first.Index)
	}
	if !first.From.Equal(d(2026, time.August, 14)) {
		t.Errorf("From = %v, want 2026-08-14", first.From)
	}
	if !first.Until.Equal(d(2026, time.August, 31)) {
		t.Errorf("Until = %v, want 2026-08-31", first.Until)
	}
}

func TestLaterPeriodsAreWholeCalendarMonths(t *testing.T) {
	got := Periods(d(2026, time.August, 14), PeriodCount)
	second := got[1]
	if !second.From.Equal(d(2026, time.September, 1)) || !second.Until.Equal(d(2026, time.September, 30)) {
		t.Errorf("period 2 = %v..%v, want 2026-09-01..2026-09-30", second.From, second.Until)
	}
	last := got[PeriodCount-1]
	if !last.From.Equal(d(2027, time.July, 1)) || !last.Until.Equal(d(2027, time.July, 31)) {
		t.Errorf("period 12 = %v..%v, want 2027-07-01..2027-07-31", last.From, last.Until)
	}
}

func TestInstallOnLastDayGivesOneDayFirstPeriod(t *testing.T) {
	got := Periods(d(2026, time.September, 30), PeriodCount)
	if !got[0].From.Equal(d(2026, time.September, 30)) || !got[0].Until.Equal(d(2026, time.September, 30)) {
		t.Errorf("period 1 = %v..%v, want a single day 2026-09-30", got[0].From, got[0].Until)
	}
	if !got[1].From.Equal(d(2026, time.October, 1)) {
		t.Errorf("period 2 starts %v, want 2026-10-01", got[1].From)
	}
}

func TestInstallOnTheThirtyFirst(t *testing.T) {
	got := Periods(d(2026, time.January, 31), PeriodCount)
	if !got[0].Until.Equal(d(2026, time.January, 31)) {
		t.Errorf("period 1 ends %v, want 2026-01-31", got[0].Until)
	}
	// February must not be skipped by naive month addition.
	if !got[1].From.Equal(d(2026, time.February, 1)) || !got[1].Until.Equal(d(2026, time.February, 28)) {
		t.Errorf("period 2 = %v..%v, want all of February 2026", got[1].From, got[1].Until)
	}
}

func TestLeapYearFebruary(t *testing.T) {
	got := Periods(d(2028, time.January, 5), PeriodCount)
	if !got[1].Until.Equal(d(2028, time.February, 29)) {
		t.Errorf("period 2 ends %v, want 2028-02-29", got[1].Until)
	}
}

func TestPeriodsAreGaplessNonOverlappingAndOrdered(t *testing.T) {
	got := Periods(d(2026, time.November, 20), PeriodCount)
	for i, p := range got {
		if p.Index != i+1 {
			t.Errorf("period at slot %d has Index %d", i, p.Index)
		}
		if p.Until.Before(p.From) {
			t.Errorf("period %d ends before it starts: %v..%v", p.Index, p.From, p.Until)
		}
		if i > 0 {
			wantStart := got[i-1].Until.NextDay()
			if !p.From.Equal(wantStart) {
				t.Errorf("period %d starts %v, want %v — periods must be gapless", p.Index, p.From, wantStart)
			}
		}
	}
}

func TestPeriodsZeroOrNegativeReturnsEmpty(t *testing.T) {
	if got := Periods(d(2026, time.August, 14), 0); len(got) != 0 {
		t.Errorf("Periods(_, 0) returned %d periods, want 0", len(got))
	}
}
```

The gapless test needs one more `Date` helper. Add it to `internal/boulevard/date.go` as part of this task:

```go
// NextDay returns the day after d. Every period ends on a month boundary, so
// in practice this is the first of the next month; the general form keeps
// the gapless-period test honest rather than tautological.
func (d Date) NextDay() Date {
	return DateFromTime(d.time().AddDate(0, 0, 1))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tokens/`
Expected: FAIL — `undefined: Periods`

- [ ] **Step 3: Implement period assignment**

Create `internal/tokens/periods.go`:

```go
package tokens

import "github.com/gumptionthomas/boulevard/internal/boulevard"

// PeriodCount is the number of cards in a booklet (DESIGN.md §4).
const PeriodCount = 12

type Period struct {
	Index int
	From  boulevard.Date
	Until boulevard.Date
}

// Periods assigns explicit calendar periods starting at install.
//
// Period 1 runs from the install date to the end of that calendar month,
// however short. Periods 2..n are whole calendar months. The short first
// period is deliberate: it aligns every later card to a month boundary.
//
// Periods are stored, never derived from a clock at validation time —
// DESIGN.md §4 rejects TOTP-style derivation because drift, DST, and a late
// card swap all become silent auth failures with no recovery path.
func Periods(install boulevard.Date, n int) []Period {
	if n <= 0 {
		return nil
	}
	out := make([]Period, 0, n)
	out = append(out, Period{Index: 1, From: install, Until: install.LastOfMonth()})

	cur := install.NextMonth() // always the first of the following month
	for i := 2; i <= n; i++ {
		out = append(out, Period{Index: i, From: cur, Until: cur.LastOfMonth()})
		cur = cur.NextMonth()
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tokens/ -v`
Expected: PASS, all cases

- [ ] **Step 5: Commit**

```bash
git add internal/tokens/periods.go internal/tokens/periods_test.go internal/boulevard/date.go
git commit -m "feat: explicit calendar period assignment for tokens"
```

---

## Task 6: SQLite store

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.sql`, `internal/store/library.go`, `internal/store/token.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `boulevard.Library`, `boulevard.LibraryID`, `boulevard.Token`, `boulevard.Date`, `boulevard.ParseDate`
- Produces:
  - `func Open(path string) (*Store, error)` — creates the schema if absent
  - `func (s *Store) Close() error`
  - `func (s *Store) CreateLibrary(ctx context.Context, lib boulevard.Library) error`
  - `func (s *Store) LibraryBySlug(ctx context.Context, slug string) (boulevard.Library, error)` — returns `ErrNotFound` when absent
  - `func (s *Store) UpdateLibrary(ctx context.Context, lib boulevard.Library) error`
  - `func (s *Store) InsertTokens(ctx context.Context, id boulevard.LibraryID, toks []boulevard.Token) error`
  - `func (s *Store) TokensForLibrary(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Token, error)` — ordered by `period_index`
  - `var ErrNotFound = errors.New("not found")`

**Every method takes a `LibraryID` or a slug explicitly.** There is no ambient current-library value. This is the v1 obligation from DESIGN.md §10 that makes the host layer reachable without a refactor.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/store_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL — `undefined: Open`

- [ ] **Step 3: Write the schema**

Create `internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS libraries (
    id             TEXT PRIMARY KEY,
    slug           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    location_label TEXT NOT NULL,
    base_url       TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id),
    secret        TEXT NOT NULL UNIQUE,
    period_index  INTEGER NOT NULL,
    valid_from    TEXT NOT NULL,
    valid_until   TEXT NOT NULL,
    state         TEXT NOT NULL,
    first_seen_at TEXT,
    created_at    TEXT NOT NULL,
    UNIQUE (library_id, period_index)
);

CREATE INDEX IF NOT EXISTS idx_tokens_library ON tokens(library_id, period_index);
```

- [ ] **Step 4: Implement the store**

Create `internal/store/store.go`:

```go
// Package store is the SQLite persistence layer.
//
// Every method takes a library identifier explicitly. There is no ambient
// "current library" value anywhere — DESIGN.md §10 requires that the v1
// single-library build never assume a singleton, so the host layer is
// reachable later without a refactor.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver; cgo would break the static binary
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

// Open opens or creates the database and applies the schema.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	// A single connection for writes. Concurrent writers on SQLite produce
	// SQLITE_BUSY, which DESIGN.md §2 names as a known hazard.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
```

Create `internal/store/library.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func (s *Store) CreateLibrary(ctx context.Context, lib boulevard.Library) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO libraries (id, slug, name, location_label, base_url, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		string(lib.ID), lib.Slug, lib.Name, lib.LocationLabel, lib.BaseURL,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create library %q: %w", lib.Slug, err)
	}
	return nil
}

func (s *Store) LibraryBySlug(ctx context.Context, slug string) (boulevard.Library, error) {
	var lib boulevard.Library
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, location_label, base_url FROM libraries WHERE slug = ?`, slug).
		Scan(&id, &lib.Slug, &lib.Name, &lib.LocationLabel, &lib.BaseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return boulevard.Library{}, fmt.Errorf("library %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return boulevard.Library{}, fmt.Errorf("look up library %q: %w", slug, err)
	}
	lib.ID = boulevard.LibraryID(id)
	return lib, nil
}

// UpdateLibrary overwrites the mutable fields. The ID is the match key and
// never changes — printed artifacts and copied database files depend on it.
func (s *Store) UpdateLibrary(ctx context.Context, lib boulevard.Library) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE libraries SET slug = ?, name = ?, location_label = ?, base_url = ? WHERE id = ?`,
		lib.Slug, lib.Name, lib.LocationLabel, lib.BaseURL, string(lib.ID))
	if err != nil {
		return fmt.Errorf("update library %q: %w", lib.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update library %q: %w", lib.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update library %q: %w", lib.ID, ErrNotFound)
	}
	return nil
}
```

Create `internal/store/token.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// InsertTokens writes a whole booklet's worth of tokens in one transaction.
// A partial booklet is never useful, so the batch is all-or-nothing.
func (s *Store) InsertTokens(ctx context.Context, id boulevard.LibraryID, toks []boulevard.Token) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token insert: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO tokens (id, library_id, secret, period_index, valid_from, valid_until, state, first_seen_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`)
	if err != nil {
		return fmt.Errorf("prepare token insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, tok := range toks {
		if tok.LibraryID != id {
			return fmt.Errorf("token %d belongs to library %q, not %q", tok.PeriodIndex, tok.LibraryID, id)
		}
		if _, err := stmt.ExecContext(ctx,
			tok.ID, string(id), tok.Secret, tok.PeriodIndex,
			tok.ValidFrom.String(), tok.ValidUntil.String(), string(tok.State), now,
		); err != nil {
			return fmt.Errorf("insert token for period %d: %w", tok.PeriodIndex, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tokens: %w", err)
	}
	return nil
}

func (s *Store) TokensForLibrary(ctx context.Context, id boulevard.LibraryID) ([]boulevard.Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, secret, period_index, valid_from, valid_until, state, first_seen_at
		   FROM tokens WHERE library_id = ? ORDER BY period_index`, string(id))
	if err != nil {
		return nil, fmt.Errorf("list tokens for library %q: %w", id, err)
	}
	defer rows.Close()

	var out []boulevard.Token
	for rows.Next() {
		var (
			tok       boulevard.Token
			from, til string
			state     string
			seen      *string
		)
		if err := rows.Scan(&tok.ID, &tok.Secret, &tok.PeriodIndex, &from, &til, &state, &seen); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		if tok.ValidFrom, err = boulevard.ParseDate(from); err != nil {
			return nil, fmt.Errorf("token %s valid_from: %w", tok.ID, err)
		}
		if tok.ValidUntil, err = boulevard.ParseDate(til); err != nil {
			return nil, fmt.Errorf("token %s valid_until: %w", tok.ID, err)
		}
		tok.LibraryID = id
		tok.State = boulevard.TokenState(state)
		if seen != nil {
			t, err := time.Parse(time.RFC3339, *seen)
			if err != nil {
				return nil, fmt.Errorf("token %s first_seen_at: %w", tok.ID, err)
			}
			tok.FirstSeenAt = &t
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

If `TestWALModeIsEnabled` fails, the driver's DSN pragma syntax differs — check with `go doc modernc.org/sqlite` and adjust the DSN in `Open`, not the test.

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "feat: library-scoped SQLite store with host-wide unique secrets"
```

---

## Task 7: QR encoding

**Files:**
- Create: `internal/booklet/qr.go`
- Test: `internal/booklet/qr_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Code struct { Modules [][]bool; Size int; Version int }`
  - `func Encode(payload string) (Code, error)` — ECC level Q, quiet zone stripped
  - `const MaxComfortableVersion = 8`
  - `func (c Code) TooDense() bool`

**Why ECC Q:** a card taped inside a box door through a Minnesota winter gets damp and creased. A steward cannot distinguish a damaged QR from a revoked one, so mid-month failure is the expensive outcome.

**Why the quiet zone is stripped:** the card layout provides more than four modules of white on every side (13 pt of padding at ~2.24 pt per module is ~5.8 modules), so a bitmap-embedded quiet zone would only shrink the printed code.

- [ ] **Step 1: Confirm the library's actual API before writing against it**

```bash
go doc github.com/skip2/go-qrcode
go doc github.com/skip2/go-qrcode.QRCode
go doc github.com/skip2/go-qrcode.New
go doc github.com/skip2/go-qrcode.QRCode.Bitmap
```

Expected: `New(content string, level RecoveryLevel) (*QRCode, error)` and `Bitmap() [][]bool`. Confirm the `RecoveryLevel` constant names (`Low`, `Medium`, `High`, `Highest`) — **`Medium` maps to ECC M and `High` maps to ECC Q**; the names do not match the ECC letters. If the package is unavailable or unmaintained, substitute another pure-Go QR library exposing a module bitmap and ECC selection, and note the swap in the commit message.

- [ ] **Step 2: Write the failing tests**

Create `internal/booklet/qr_test.go`:

```go
package booklet

import (
	"strings"
	"testing"
)

func TestEncodeProducesSquareBitmap(t *testing.T) {
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if c.Size <= 0 {
		t.Fatalf("Size = %d, want positive", c.Size)
	}
	if len(c.Modules) != c.Size {
		t.Errorf("bitmap has %d rows, want %d", len(c.Modules), c.Size)
	}
	for i, row := range c.Modules {
		if len(row) != c.Size {
			t.Errorf("row %d has %d columns, want %d", i, len(row), c.Size)
		}
	}
}

func TestSizeMatchesVersionFormula(t *testing.T) {
	// A QR symbol of version v is 4v+17 modules on a side.
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if want := 4*c.Version + 17; c.Size != want {
		t.Errorf("Size = %d, want %d for version %d", c.Size, want, c.Version)
	}
}

func TestQuietZoneIsStripped(t *testing.T) {
	// A stripped symbol always has a dark module at its top-left corner —
	// that is the corner of the finder pattern. With a quiet zone present
	// the corner would be light.
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Modules[0][0] {
		t.Error("top-left module is light; the quiet zone was not stripped")
	}
}

func TestTypicalPayloadStaysComfortable(t *testing.T) {
	c, err := Encode("https://boulevard.example.org/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if c.TooDense() {
		t.Errorf("version %d is flagged too dense for a typical payload", c.Version)
	}
}

func TestLongBaseURLIsFlaggedTooDense(t *testing.T) {
	long := "https://" + strings.Repeat("verylongsubdomain.", 8) + "example.org"
	c, err := Encode(long + "/s/K7QFM8X2N4TJ9WPR3VYB6HZQ5C")
	if err != nil {
		t.Fatal(err)
	}
	if !c.TooDense() {
		t.Errorf("version %d should be flagged; a long base URL shrinks each module below reliable scanning size", c.Version)
	}
}

func TestEncodeRejectsEmptyPayload(t *testing.T) {
	if _, err := Encode(""); err == nil {
		t.Error("Encode(\"\") succeeded, want error")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/booklet/`
Expected: FAIL — `undefined: Encode`

- [ ] **Step 4: Implement QR encoding**

Create `internal/booklet/qr.go`:

```go
// Package booklet plans and renders the printable token booklet.
package booklet

import (
	"errors"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// MaxComfortableVersion bounds symbol density. At the card's 83 pt QR, a
// version 8 symbol (49 modules) is about 0.59 mm per module — the point
// where phone scanning in poor light starts to suffer. Anything denser
// means the base URL is too long to print reliably at this size.
const MaxComfortableVersion = 8

// Code is a QR symbol as a bitmap of dark modules, quiet zone removed.
type Code struct {
	Modules [][]bool
	Size    int // modules per side
	Version int
}

func (c Code) TooDense() bool { return c.Version > MaxComfortableVersion }

// Encode builds a symbol at ECC level Q (~25% recoverable).
//
// A card lives taped inside a box door through a winter; it gets damp and
// creased. A steward cannot tell a damaged QR from a revoked one, so
// durability is worth the extra modules.
func Encode(payload string) (Code, error) {
	if payload == "" {
		return Code{}, errors.New("qr payload is empty")
	}
	// go-qrcode's "High" is ECC level Q. The constant names do not match
	// the ECC letters — see the package docs.
	q, err := qrcode.New(payload, qrcode.High)
	if err != nil {
		return Code{}, fmt.Errorf("encode qr for %q: %w", payload, err)
	}

	full := q.Bitmap()
	// Bitmap() includes a 4-module quiet zone on every side.
	const quiet = 4
	size := len(full) - 2*quiet
	if size < 21 { // a version 1 symbol is 21 modules
		return Code{}, fmt.Errorf("qr bitmap is %d modules after stripping the quiet zone", size)
	}

	mods := make([][]bool, size)
	for y := 0; y < size; y++ {
		row := make([]bool, size)
		copy(row, full[y+quiet][quiet:quiet+size])
		mods[y] = row
	}

	return Code{Modules: mods, Size: size, Version: (size - 17) / 4}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/booklet/ -v`
Expected: PASS

If `TestQuietZoneIsStripped` fails, the library's quiet zone is not 4 modules. Determine the real border by encoding a known payload and printing `len(full)`, then compare against `4*version+17`; adjust the `quiet` constant.

- [ ] **Step 6: Commit**

```bash
git add internal/booklet/qr.go internal/booklet/qr_test.go
git commit -m "feat: QR encoding at ECC Q with a density guard"
```

---

## Task 8: Printed geometry and the layout plan

**Files:**
- Create: `internal/booklet/geometry.go`, `internal/booklet/plan.go`
- Test: `internal/booklet/plan_test.go`

**Interfaces:**
- Consumes: `boulevard.Library`, `boulevard.Token`, `version` metadata
- Produces:
  - `type Rect struct { X, Y, W, H float64 }`
  - `type PlacedCard struct { Rect Rect; Month string; Year int; From, Until boulevard.Date; Index, Of int; Payload string }`
  - `type Cover struct { Rect Rect; LibraryName, LocationLabel, SignPayload, SourceURL, BuildLine string }`
  - `type Sheet struct { Cards []PlacedCard; Cover *Cover }`
  - `type Plan struct { Sheets []Sheet }`
  - `type Input struct { Library boulevard.Library; Tokens []boulevard.Token; SourceURL, BuildLine string }`
  - `func BuildPlan(in Input) (Plan, error)`
  - Geometry constants listed in the code below

- [ ] **Step 1: Write the geometry constants**

Create `internal/booklet/geometry.go`:

```go
package booklet

// All dimensions are PostScript points: 72 pt = 1 inch.
const (
	PageW = 612.0 // US Letter, 8.5 in
	PageH = 792.0 // US Letter, 11 in

	// A business card. Sleeves and holders are commodity-available in
	// exactly this size, and a small card resists being scanned from a
	// passing car — which protects presence as the credential.
	CardW = 252.0 // 3.5 in
	CardH = 144.0 // 2 in

	Cols = 2
	Rows = 5

	GridW = CardW * Cols // 504
	GridH = CardH * Rows // 720

	MarginX = (PageW - GridW) / 2 // 54 pt, 0.75 in
	MarginY = (PageH - GridH) / 2 // 36 pt, 0.5 in

	// Card face.
	BarH       = 19.0 // purpose bar
	CardPadX   = 13.0
	CardQR     = 83.0  // 1.15 in
	TextColX   = 107.5 // from the card's left edge
	HairlineW  = 0.4

	// The cover occupies the first four rows of sheet 2; the last two
	// cards sit in row 5. One horizontal cut at CoverH + MarginY frees
	// the cover intact.
	CoverRows = 4
	CoverH    = CardH * CoverRows // 576

	// Browse sign, cut out of the cover.
	SignW  = 302.4 // 4.2 in
	SignH  = 158.4 // 2.2 in
	SignQR = 108.0 // 1.5 in
)

// Copy that must not drift. DESIGN.md §4, as amended.
const (
	CardVerb      = "SCAN TO LEAVE OR TAKE"
	SignVerb      = "Scan to browse the shelf"
	SignSubtitle  = "Anyone, anywhere, anytime. No code needed."
	SignageLine   = "Take something. Leave something. Scan to leave."
	SleeveWarning = "Mount the browse sign in a sleeve. Never laminate or engrave it — a hosting arrangement can end, and this sign must stay replaceable."
)

type Rect struct{ X, Y, W, H float64 }

// CardRect returns the position of card i (zero-based) within a sheet grid.
func CardRect(i int) Rect {
	col := i % Cols
	row := i / Cols
	return Rect{
		X: MarginX + float64(col)*CardW,
		Y: MarginY + float64(row)*CardH,
		W: CardW,
		H: CardH,
	}
}
```

- [ ] **Step 2: Write the failing plan tests**

Create `internal/booklet/plan_test.go`:

```go
package booklet

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

func testInput(t *testing.T) Input {
	t.Helper()
	id, err := boulevard.NewLibraryID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lib := boulevard.Library{
		ID: id, Slug: "fairview", Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       "https://boulevard.example.org",
	}
	periods := tokens.Periods(boulevard.NewDate(2026, time.August, 14), tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, boulevard.Token{
			LibraryID: id, Secret: secret, PeriodIndex: p.Index,
			ValidFrom: p.From, ValidUntil: p.Until, State: boulevard.TokenPending,
		})
	}
	return Input{
		Library: lib, Tokens: toks,
		SourceURL: "https://github.com/gumptionthomas/boulevard",
		BuildLine: "boulevard 0.1.0 (abc1234)",
	}
}

func TestPlanHasTwoSheets(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Sheets) != 2 {
		t.Fatalf("got %d sheets, want 2", len(p.Sheets))
	}
	if len(p.Sheets[0].Cards) != 10 {
		t.Errorf("sheet 1 has %d cards, want 10", len(p.Sheets[0].Cards))
	}
	if len(p.Sheets[1].Cards) != 2 {
		t.Errorf("sheet 2 has %d cards, want 2", len(p.Sheets[1].Cards))
	}
}

func TestCoverIsOnExactlyOneSheet(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	covers := 0
	for _, s := range p.Sheets {
		if s.Cover != nil {
			covers++
		}
	}
	if covers != 1 {
		t.Errorf("found %d covers, want exactly 1", covers)
	}
	if p.Sheets[0].Cover != nil {
		t.Error("cover must not be on sheet 1 — that sheet is a clean 2x5 grid of cards")
	}
}

func TestEverySecretAppearsExactlyOnce(t *testing.T) {
	in := testInput(t)
	p, _ := BuildPlan(in)
	count := map[string]int{}
	for _, s := range p.Sheets {
		for _, c := range s.Cards {
			count[c.Payload]++
		}
	}
	if len(count) != tokens.PeriodCount {
		t.Errorf("got %d distinct payloads, want %d", len(count), tokens.PeriodCount)
	}
	for _, tok := range in.Tokens {
		want := in.Library.BaseURL + "/s/" + tok.Secret
		if count[want] != 1 {
			t.Errorf("payload for period %d appears %d times, want 1", tok.PeriodIndex, count[want])
		}
	}
}

func TestCardsCarryOrderingMetadata(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	var seen []int
	for _, s := range p.Sheets {
		for _, c := range s.Cards {
			seen = append(seen, c.Index)
			if c.Of != tokens.PeriodCount {
				t.Errorf("card %d says 'of %d', want %d", c.Index, c.Of, tokens.PeriodCount)
			}
		}
	}
	for i, idx := range seen {
		if idx != i+1 {
			t.Errorf("card at position %d has Index %d; cards must be laid out in period order", i, idx)
		}
	}
}

func TestFirstCardCarriesItsMonthAndDates(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	c := p.Sheets[0].Cards[0]
	if c.Month != "August" || c.Year != 2026 {
		t.Errorf("first card = %s %d, want August 2026", c.Month, c.Year)
	}
	if !c.From.Equal(boulevard.NewDate(2026, time.August, 14)) {
		t.Errorf("From = %v, want 2026-08-14", c.From)
	}
	if !c.Until.Equal(boulevard.NewDate(2026, time.August, 31)) {
		t.Errorf("Until = %v, want 2026-08-31", c.Until)
	}
}

func TestEveryRectFitsInsideThePrintableArea(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	for si, s := range p.Sheets {
		for _, c := range s.Cards {
			assertInsidePage(t, si, "card", c.Rect)
		}
		if s.Cover != nil {
			assertInsidePage(t, si, "cover", s.Cover.Rect)
		}
	}
}

func assertInsidePage(t *testing.T, sheet int, what string, r Rect) {
	t.Helper()
	if r.X < MarginX || r.Y < MarginY {
		t.Errorf("sheet %d %s at (%.1f,%.1f) starts inside the margin", sheet, what, r.X, r.Y)
	}
	if r.X+r.W > PageW-MarginX+0.01 {
		t.Errorf("sheet %d %s right edge %.1f exceeds %.1f", sheet, what, r.X+r.W, PageW-MarginX)
	}
	if r.Y+r.H > PageH-MarginY+0.01 {
		t.Errorf("sheet %d %s bottom edge %.1f exceeds %.1f", sheet, what, r.Y+r.H, PageH-MarginY)
	}
}

func TestCoverSitsAboveTheCardRowOnSheetTwo(t *testing.T) {
	p, _ := BuildPlan(testInput(t))
	s := p.Sheets[1]
	cut := s.Cover.Rect.Y + s.Cover.Rect.H
	for _, c := range s.Cards {
		if c.Rect.Y < cut-0.01 {
			t.Errorf("card at y=%.1f overlaps the cover, which ends at y=%.1f", c.Rect.Y, cut)
		}
	}
	if cut != MarginY+CoverH {
		t.Errorf("cut line at %.1f, want %.1f", cut, MarginY+CoverH)
	}
}

func TestBuildPlanRejectsWrongTokenCount(t *testing.T) {
	in := testInput(t)
	in.Tokens = in.Tokens[:5]
	if _, err := BuildPlan(in); err == nil {
		t.Error("BuildPlan accepted 5 tokens, want an error")
	}
}

func TestSignPayloadIsTheBareBaseURL(t *testing.T) {
	in := testInput(t)
	p, _ := BuildPlan(in)
	if got := p.Sheets[1].Cover.SignPayload; got != in.Library.BaseURL {
		t.Errorf("SignPayload = %q, want the bare base URL %q", got, in.Library.BaseURL)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/booklet/`
Expected: FAIL — `undefined: BuildPlan`

- [ ] **Step 4: Implement the plan builder**

Create `internal/booklet/plan.go`:

```go
package booklet

import (
	"fmt"
	"sort"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// PlacedCard is one monthly card and where it sits on its sheet.
type PlacedCard struct {
	Rect    Rect
	Month   string
	Year    int
	From    boulevard.Date
	Until   boulevard.Date
	Index   int // 1-based
	Of      int
	Payload string
}

type Cover struct {
	Rect          Rect
	LibraryName   string
	LocationLabel string
	SignPayload   string
	SourceURL     string
	BuildLine     string
}

type Sheet struct {
	Cards []PlacedCard
	Cover *Cover
}

type Plan struct{ Sheets []Sheet }

type Input struct {
	Library   boulevard.Library
	Tokens    []boulevard.Token
	SourceURL string
	BuildLine string
}

// CardsPerSheet is a full grid.
const CardsPerSheet = Cols * Rows // 10

// BuildPlan computes pure layout data. It never touches a PDF library, so
// every placement rule here is unit-testable on its own.
func BuildPlan(in Input) (Plan, error) {
	if len(in.Tokens) != tokens.PeriodCount {
		return Plan{}, fmt.Errorf("booklet needs exactly %d tokens, got %d", tokens.PeriodCount, len(in.Tokens))
	}
	if in.Library.BaseURL == "" {
		return Plan{}, fmt.Errorf("library %q has no base URL", in.Library.Slug)
	}

	toks := append([]boulevard.Token(nil), in.Tokens...)
	sort.Slice(toks, func(i, j int) bool { return toks[i].PeriodIndex < toks[j].PeriodIndex })

	sheet1 := Sheet{Cards: make([]PlacedCard, 0, CardsPerSheet)}
	for i := 0; i < CardsPerSheet; i++ {
		sheet1.Cards = append(sheet1.Cards, placeCard(toks[i], CardRect(i), in.Library.BaseURL))
	}

	// Sheet 2: the cover fills rows 1..4, the remaining cards sit in row 5.
	// That gives one horizontal cut and one vertical cut, and the cover
	// survives intact.
	sheet2 := Sheet{
		Cover: &Cover{
			Rect:          Rect{X: MarginX, Y: MarginY, W: GridW, H: CoverH},
			LibraryName:   in.Library.Name,
			LocationLabel: in.Library.LocationLabel,
			SignPayload:   in.Library.BaseURL,
			SourceURL:     in.SourceURL,
			BuildLine:     in.BuildLine,
		},
	}
	for i := CardsPerSheet; i < len(toks); i++ {
		slot := CardsPerSheet - Cols + (i - CardsPerSheet) // final row of the grid
		sheet2.Cards = append(sheet2.Cards, placeCard(toks[i], CardRect(slot), in.Library.BaseURL))
	}

	return Plan{Sheets: []Sheet{sheet1, sheet2}}, nil
}

func placeCard(tok boulevard.Token, r Rect, baseURL string) PlacedCard {
	return PlacedCard{
		Rect:    r,
		Month:   tok.ValidFrom.MonthName(),
		Year:    tok.ValidFrom.Year,
		From:    tok.ValidFrom,
		Until:   tok.ValidUntil,
		Index:   tok.PeriodIndex,
		Of:      tokens.PeriodCount,
		Payload: baseURL + "/s/" + tok.Secret,
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/booklet/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/booklet/geometry.go internal/booklet/plan.go internal/booklet/plan_test.go
git commit -m "feat: printed geometry constants and pure layout planning"
```

---

## Task 9: Render the card face

**Files:**
- Create: `internal/booklet/render.go`, `internal/booklet/card.go`
- Test: `internal/booklet/render_test.go`

**Interfaces:**
- Consumes: `Plan`, `PlacedCard`, `Code` (Tasks 7–8)
- Produces:
  - `type Renderer struct { CreationDate time.Time }`
  - `func (r Renderer) Render(p Plan) ([]byte, error)`
  - `func drawTracked(pdf *fpdf.Fpdf, x, y float64, text string, tracking float64)` — letterspaced text, since fpdf has no native tracking
  - `func drawQR(pdf *fpdf.Fpdf, c Code, x, y, size float64)` — vector rects, horizontal runs merged

**Determinism:** `CreationDate` is a field, not `time.Now()`. Combined with injected token secrets, identical inputs produce identical bytes.

- [ ] **Step 1: Confirm the fpdf API**

```bash
go doc github.com/go-pdf/fpdf.New
go doc github.com/go-pdf/fpdf.Fpdf.SetCreationDate
go doc github.com/go-pdf/fpdf.Fpdf.Rect
go doc github.com/go-pdf/fpdf.Fpdf.Output
```

Expected: `New(orientationStr, unitStr, sizeStr, fontDirStr string) *Fpdf` accepting `"pt"` and `"Letter"`, and `SetCreationDate(t time.Time)`.

Core fonts only — `Helvetica`, `Times`, `Courier`. **Do not embed fonts:** it bloats the binary and makes output less deterministic.

- [ ] **Step 2: Write the failing tests**

Create `internal/booklet/render_test.go`:

```go
package booklet

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixedRenderer() Renderer {
	return Renderer{CreationDate: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)}
}

func TestRenderProducesAPDF(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Errorf("output does not start with a PDF header: %q", out[:min(8, len(out))])
	}
	if len(out) < 1000 {
		t.Errorf("output is %d bytes, suspiciously small for a two-page booklet", len(out))
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	// Golden-file testing is only possible if identical inputs produce
	// identical bytes. Creation date must be injected, never ambient.
	in := testInput(t)
	p, _ := BuildPlan(in)
	a, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two renders of the same plan differ; something ambient (time, map order, randomness) leaked in")
	}
}

func TestRenderGolden(t *testing.T) {
	p, err := BuildPlan(fixedInput(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "booklet.pdf")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/booklet -update` to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("rendered output differs from %s (got %d bytes, want %d). "+
			"If the change is intended, re-run with -update and inspect the PDF by eye.",
			golden, len(got), len(want))
	}
}

func TestRenderRejectsAnEmptyPlan(t *testing.T) {
	if _, err := fixedRenderer().Render(Plan{}); err == nil {
		t.Error("Render(Plan{}) succeeded, want an error")
	}
}
```

The golden test needs fully fixed input. Add this helper to `plan_test.go`:

```go
// fixedInput mirrors testInput but draws secrets from a fixed byte source so
// the golden PDF is stable across runs.
func fixedInput(t *testing.T) Input {
	t.Helper()
	src := bytes.NewReader(bytes.Repeat([]byte("boulevard-golden!"), 64))
	id, err := boulevard.NewLibraryID(src)
	if err != nil {
		t.Fatal(err)
	}
	lib := boulevard.Library{
		ID: id, Slug: "fairview", Name: "The Fairview Boulevard",
		LocationLabel: "4th & Fairview, Minneapolis",
		BaseURL:       "https://boulevard.example.org",
	}
	periods := tokens.Periods(boulevard.NewDate(2026, time.August, 14), tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(src)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, boulevard.Token{
			LibraryID: id, Secret: secret, PeriodIndex: p.Index,
			ValidFrom: p.From, ValidUntil: p.Until, State: boulevard.TokenPending,
		})
	}
	return Input{
		Library: lib, Tokens: toks,
		SourceURL: "https://github.com/gumptionthomas/boulevard",
		BuildLine: "boulevard 0.1.0 (abc1234)",
	}
}
```

Add `"bytes"` and `"flag"` to the relevant import blocks.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/booklet/`
Expected: FAIL — `undefined: Renderer`

- [ ] **Step 4: Implement the renderer core**

Create `internal/booklet/render.go`:

```go
package booklet

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// Ink and paper. Kept as a palette so the card and the sign stay consistent.
var (
	inkR, inkG, inkB       = 20, 19, 13    // #14130D
	paperR, paperG, paperB = 253, 252, 249 // #FDFCF9
	ruleR, ruleG, ruleB    = 201, 195, 178
)

// Renderer draws a Plan to PDF bytes.
//
// CreationDate is a field rather than time.Now() so that identical inputs
// render to identical bytes, which is what makes golden testing possible.
type Renderer struct {
	CreationDate time.Time
}

func (r Renderer) Render(p Plan) ([]byte, error) {
	if len(p.Sheets) == 0 {
		return nil, errors.New("plan has no sheets")
	}

	pdf := fpdf.New("P", "pt", "Letter", "")
	pdf.SetCreationDate(r.CreationDate)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)

	for _, sheet := range p.Sheets {
		pdf.AddPage()
		if sheet.Cover != nil {
			drawCover(pdf, *sheet.Cover)
		}
		for _, c := range sheet.Cards {
			if err := drawCard(pdf, c); err != nil {
				return nil, fmt.Errorf("draw card %d: %w", c.Index, err)
			}
		}
		drawCutMarks(pdf, sheet)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// drawQR paints a symbol as vector rectangles, merging horizontal runs of
// dark modules into single rects. Vector keeps the code crisp at any print
// size; run-merging keeps the file small — a naive per-module version emits
// well over ten thousand rectangles per booklet.
func drawQR(pdf *fpdf.Fpdf, c Code, x, y, size float64) {
	m := size / float64(c.Size)
	pdf.SetFillColor(inkR, inkG, inkB)
	for row := 0; row < c.Size; row++ {
		col := 0
		for col < c.Size {
			if !c.Modules[row][col] {
				col++
				continue
			}
			run := 1
			for col+run < c.Size && c.Modules[row][col+run] {
				run++
			}
			pdf.Rect(x+float64(col)*m, y+float64(row)*m, float64(run)*m, m, "F")
			col += run
		}
	}
}

// drawTracked renders letterspaced text. fpdf has no native tracking, so
// characters are placed individually.
func drawTracked(pdf *fpdf.Fpdf, x, y float64, text string, tracking float64) {
	cur := x
	for _, ch := range text {
		s := string(ch)
		pdf.Text(cur, y, s)
		cur += pdf.GetStringWidth(s) + tracking
	}
}

// trackedWidth is the width drawTracked will occupy, for centering.
func trackedWidth(pdf *fpdf.Fpdf, text string, tracking float64) float64 {
	var w float64
	for _, ch := range text {
		w += pdf.GetStringWidth(string(ch)) + tracking
	}
	if w > 0 {
		w -= tracking // no trailing gap
	}
	return w
}

// drawCutMarks draws hairlines on the shared card edges plus ticks into the
// margins, so a steward can lay a straightedge across the page. Scissors,
// not a guillotine (DESIGN.md §4).
func drawCutMarks(pdf *fpdf.Fpdf, sheet Sheet) {
	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)

	const tick = 12.0
	for _, c := range sheet.Cards {
		pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "D")
		pdf.Line(c.Rect.X, c.Rect.Y, c.Rect.X-tick, c.Rect.Y)
		pdf.Line(c.Rect.X+c.Rect.W, c.Rect.Y, c.Rect.X+c.Rect.W+tick, c.Rect.Y)
	}
	if sheet.Cover != nil {
		// The single horizontal cut that frees the cover.
		cut := sheet.Cover.Rect.Y + sheet.Cover.Rect.H
		pdf.Line(MarginX-tick, cut, PageW-MarginX+tick, cut)
	}
}
```

- [ ] **Step 5: Implement the card face**

Create `internal/booklet/card.go`:

```go
package booklet

import (
	"fmt"

	"github.com/go-pdf/fpdf"
)

// drawCard paints one monthly card: a dark purpose bar over a month-forward
// body with the QR on the left.
//
// The bar earns its space twice — it tells a visitor what scanning does, and
// it makes the card unmistakable against the light, landscape browse sign.
func drawCard(pdf *fpdf.Fpdf, c PlacedCard) error {
	code, err := Encode(c.Payload)
	if err != nil {
		return err
	}
	if code.TooDense() {
		return fmt.Errorf("qr version %d exceeds the comfortable maximum of %d — the base URL is too long to print reliably at %.0f pt",
			code.Version, MaxComfortableVersion, CardQR)
	}

	// Paper, so the card reads as an object rather than a hole in the page.
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "F")

	// Purpose bar.
	pdf.SetFillColor(inkR, inkG, inkB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, BarH, "F")
	pdf.SetFont("Helvetica", "B", 7.5)
	pdf.SetTextColor(paperR, paperG, paperB)
	const tracking = 0.9
	bw := trackedWidth(pdf, CardVerb, tracking)
	drawTracked(pdf, c.Rect.X+(c.Rect.W-bw)/2, c.Rect.Y+BarH-6.5, CardVerb, tracking)

	// QR, vertically centred in the body below the bar.
	bodyY := c.Rect.Y + BarH
	bodyH := c.Rect.H - BarH
	drawQR(pdf, code, c.Rect.X+CardPadX, bodyY+(bodyH-CardQR)/2, CardQR)

	// Text column.
	pdf.SetTextColor(inkR, inkG, inkB)
	tx := c.Rect.X + TextColX

	pdf.SetFont("Times", "B", 15)
	pdf.Text(tx, bodyY+38, c.Month)

	pdf.SetFont("Times", "", 9.5)
	pdf.SetTextColor(90, 88, 80)
	pdf.Text(tx, bodyY+52, fmt.Sprintf("%d", c.Year))

	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)
	pdf.Line(tx, bodyY+60, c.Rect.X+c.Rect.W-CardPadX, bodyY+60)

	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(60, 58, 52)
	pdf.Text(tx, bodyY+74, fmt.Sprintf("%s – %s", shortDate(c.From), shortDate(c.Until)))

	pdf.SetFont("Helvetica", "", 6.5)
	pdf.SetTextColor(120, 117, 108)
	drawTracked(pdf, tx, bodyY+88, fmt.Sprintf("CARD %d OF %d", c.Index, c.Of), 0.5)

	pdf.SetTextColor(inkR, inkG, inkB)
	return nil
}

// shortDate renders "Aug 14" for the card's date range.
func shortDate(d boulevard.Date) string {
	return fmt.Sprintf("%s %d", d.Month.String()[:3], d.Day)
}
```

`card.go`'s import block is therefore:

```go
import (
	"fmt"

	"github.com/go-pdf/fpdf"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
)
```

- [ ] **Step 6: Add a temporary cover stub so the package compiles**

Task 10 replaces this. Create `internal/booklet/cover.go`:

```go
package booklet

import "github.com/go-pdf/fpdf"

func drawCover(pdf *fpdf.Fpdf, c Cover) {}
```

- [ ] **Step 7: Run the tests**

```bash
go test ./internal/booklet/ -run 'TestRenderProducesAPDF|TestRenderIsDeterministic|TestRenderRejectsAnEmptyPlan' -v
```

Expected: PASS

If `TestRenderIsDeterministic` fails, something ambient leaked in — check for `time.Now()`, map iteration, or an un-seeded random source.

- [ ] **Step 8: Eyeball the output before trusting the golden file**

```bash
go test ./internal/booklet/ -run TestRenderGolden -update
cp internal/booklet/testdata/booklet.pdf /tmp/booklet-preview.pdf
xdg-open /tmp/booklet-preview.pdf 2>/dev/null || echo "open /tmp/booklet-preview.pdf manually"
```

**Do not skip this.** A golden file that locks in a broken layout is worse than no golden file. Confirm by eye: twelve cards, purpose bar legible, month readable, QR square and not clipped, nothing overlapping.

- [ ] **Step 9: Commit**

```bash
git add internal/booklet/render.go internal/booklet/card.go internal/booklet/cover.go \
        internal/booklet/render_test.go internal/booklet/plan_test.go internal/booklet/testdata
git commit -m "feat: render monthly card faces to PDF"
```

---

## Task 10: Render the cover sheet and browse sign

**Files:**
- Modify: `internal/booklet/cover.go` (replace the stub from Task 9)
- Test: `internal/booklet/cover_test.go`

**Interfaces:**
- Consumes: `Cover`, `Code`, geometry constants
- Produces: `func drawCover(pdf *fpdf.Fpdf, c Cover)` — full implementation; `func SignRect(cover Rect) Rect`

- [ ] **Step 1: Write the failing test**

Create `internal/booklet/cover_test.go`:

```go
package booklet

import "testing"

func TestSignRectIsCenteredInsideTheCover(t *testing.T) {
	cover := Rect{X: MarginX, Y: MarginY, W: GridW, H: CoverH}
	got := SignRect(cover)

	if got.W != SignW || got.H != SignH {
		t.Errorf("sign is %.1fx%.1f, want %.1fx%.1f", got.W, got.H, SignW, SignH)
	}
	wantX := cover.X + (cover.W-SignW)/2
	if got.X != wantX {
		t.Errorf("sign X = %.2f, want %.2f (horizontally centred)", got.X, wantX)
	}
	if got.X < cover.X || got.X+got.W > cover.X+cover.W {
		t.Error("sign escapes the cover horizontally")
	}
	if got.Y < cover.Y || got.Y+got.H > cover.Y+cover.H {
		t.Error("sign escapes the cover vertically")
	}
}

func TestCoverRendersWithoutError(t *testing.T) {
	p, err := BuildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := fixedRenderer().Render(p)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A rendered cover adds a second QR and a block of copy; a booklet with
	// a stubbed-out cover is markedly smaller.
	if len(out) < 5000 {
		t.Errorf("output is %d bytes, too small to contain a rendered cover", len(out))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/booklet/ -run TestSignRect`
Expected: FAIL — `undefined: SignRect`

- [ ] **Step 3: Implement the cover**

Replace `internal/booklet/cover.go` entirely:

```go
package booklet

import (
	"github.com/go-pdf/fpdf"
)

// SignRect centres the browse sign horizontally within the cover, seated
// below the title block.
func SignRect(cover Rect) Rect {
	return Rect{
		X: cover.X + (cover.W-SignW)/2,
		Y: cover.Y + 94,
		W: SignW,
		H: SignH,
	}
}

var installSteps = []string{
	"1.  Cut the cards apart along the hairlines. Keep them in order.",
	"2.  Tape the current month's card inside the box door.",
	"3.  On the first of each month, swap in the next card.",
	"4.  While you are there, look at the hinge, the sign, and the shelf.",
	"5.  Mount the browse sign where it can be read from the sidewalk.",
}

func drawCover(pdf *fpdf.Fpdf, c Cover) {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H, "F")

	left := c.Rect.X + 28

	// Title block.
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Times", "B", 20)
	pdf.Text(left, c.Rect.Y+44, c.LibraryName)

	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(110, 107, 98)
	pdf.Text(left, c.Rect.Y+64, c.LocationLabel)

	drawBrowseSign(pdf, SignRect(c.Rect), c.SignPayload, c.LibraryName)

	// Instructions.
	instrY := SignRect(c.Rect).Y + SignH + 40
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.Text(left, instrY, "Setting up")

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 58, 52)
	for i, step := range installSteps {
		pdf.Text(left, instrY+18+float64(i)*15, step)
	}

	// The sleeve warning. DESIGN.md §10: a printed sign outlives the
	// hosting arrangement that made its URL work.
	warnY := instrY + 18 + float64(len(installSteps))*15 + 14
	pdf.SetFont("Helvetica", "I", 8.5)
	pdf.SetTextColor(110, 107, 98)
	pdf.SetXY(left, warnY)
	pdf.MultiCell(c.Rect.W-56, 11, SleeveWarning, "", "L", false)

	// Signage line, set apart.
	pdf.SetFont("Times", "", 13)
	pdf.SetTextColor(inkR, inkG, inkB)
	sw := pdf.GetStringWidth(SignageLine)
	pdf.Text(c.Rect.X+(c.Rect.W-sw)/2, c.Rect.Y+c.Rect.H-52, SignageLine)

	// Footer: the AGPL source offer, per DESIGN.md §2.
	pdf.SetFont("Helvetica", "", 7)
	pdf.SetTextColor(140, 137, 128)
	pdf.Text(left, c.Rect.Y+c.Rect.H-16, c.BuildLine+"  ·  source: "+c.SourceURL)
	pdf.SetTextColor(inkR, inkG, inkB)
}

// drawBrowseSign paints the permanent, mounted artifact: light, landscape,
// undated — deliberately unlike the dark, dated monthly card.
func drawBrowseSign(pdf *fpdf.Fpdf, r Rect, payload, libraryName string) {
	pdf.SetFillColor(paperR, paperG, paperB)
	pdf.Rect(r.X, r.Y, r.W, r.H, "F")

	pdf.SetDrawColor(ruleR, ruleG, ruleB)
	pdf.SetLineWidth(HairlineW)
	pdf.Rect(r.X, r.Y, r.W, r.H, "D")

	code, err := Encode(payload)
	if err != nil {
		return // a plan that reached rendering already validated its payloads
	}
	qrX := r.X + 20
	drawQR(pdf, code, qrX, r.Y+(r.H-SignQR)/2, SignQR)

	tx := qrX + SignQR + 20
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetFont("Times", "B", 14)
	pdf.Text(tx, r.Y+56, libraryName)

	pdf.SetFont("Helvetica", "B", 9)
	pdf.Text(tx, r.Y+78, SignVerb)

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(100, 97, 89)
	pdf.Text(tx, r.Y+94, SignSubtitle)
	pdf.SetTextColor(inkR, inkG, inkB)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/booklet/ -run 'TestSignRect|TestCoverRenders' -v`
Expected: PASS

- [ ] **Step 5: Regenerate the golden file and inspect it by eye**

```bash
go test ./internal/booklet/ -run TestRenderGolden -update
cp internal/booklet/testdata/booklet.pdf /tmp/booklet-preview.pdf
xdg-open /tmp/booklet-preview.pdf 2>/dev/null || echo "open /tmp/booklet-preview.pdf manually"
```

Confirm on sheet 2: the title, the browse sign cut-out with its own QR, five numbered steps, the sleeve warning, the signage line, and the source footer — none overlapping, all inside the cover region, and the two June/July cards below the cut line.

- [ ] **Step 6: Run the full package suite**

Run: `go test ./internal/booklet/ -v`
Expected: PASS, including `TestRenderGolden`

- [ ] **Step 7: Commit**

```bash
git add internal/booklet/cover.go internal/booklet/cover_test.go internal/booklet/testdata
git commit -m "feat: render cover sheet and permanent browse sign"
```

---

## Task 11: The booklet command

**Files:**
- Modify: `cmd/boulevard/booklet.go` (replace the Task 1 stub)
- Test: `cmd/boulevard/booklet_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2–10
- Produces: `func runBooklet(args []string) int`; `type bookletOpts struct{...}`; `func resolveLibrary(ctx, *store.Store, bookletOpts, io.Reader, io.Writer) (boulevard.Library, bool, error)`

**Behavior, per spec §9.1** — implement in this order:

1. Validate URL shape; reject path, query, fragment, credentials.
2. Resolve the hostname via DNS unless `--skip-dns`.
3. Print what will be encoded; require `y/N` unless `--yes`.
4. Open/create the DB. Look the library up **by slug**.
5. If it exists, reuse its tokens — **never re-mint**. Overwrite name and location silently; overwrite base URL only after a loud warning.
6. Refuse to overwrite an existing `--out` without `--force`.
7. Write the PDF.

- [ ] **Step 1: Write the failing tests**

Create `cmd/boulevard/booklet_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
```

Add this helper to the test file:

```go
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/boulevard/`
Expected: FAIL — `undefined: confirm`, and the stub panics

- [ ] **Step 3: Implement the command**

Replace `cmd/boulevard/booklet.go` entirely:

```go
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/booklet"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
	"github.com/gumptionthomas/boulevard/internal/version"
)

type bookletOpts struct {
	name, location, baseURL, slug string
	out, db                       string
	skipDNS, yes, force           bool
	installDate                   boulevard.Date
}

func runBooklet(args []string) int {
	fs := flag.NewFlagSet("booklet", flag.ContinueOnError)
	var o bookletOpts
	fs.StringVar(&o.name, "name", "", "library display name (required)")
	fs.StringVar(&o.location, "location", "", "human location label (required)")
	fs.StringVar(&o.baseURL, "base-url", "", "scheme and host, no path (required)")
	fs.StringVar(&o.slug, "slug", "", "URL slug (default: derived from name)")
	fs.StringVar(&o.out, "out", "boulevard-booklet.pdf", "output PDF path")
	fs.StringVar(&o.db, "db", "boulevard.db", "database path")
	fs.BoolVar(&o.skipDNS, "skip-dns", false, "skip the DNS check")
	fs.BoolVar(&o.yes, "yes", false, "skip the confirmation prompt")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing output file")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	if o.name == "" || o.location == "" || o.baseURL == "" {
		fmt.Fprintln(os.Stderr, "--name, --location and --base-url are all required")
		return exitUsage
	}

	normalized, err := boulevard.ValidateBaseURL(o.baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n     Every card and the permanent browse sign encode this host.\n", err)
		return exitUsage
	}
	o.baseURL = normalized

	if o.slug == "" {
		o.slug = boulevard.Slugify(o.name)
	}
	if o.slug == "" {
		fmt.Fprintf(os.Stderr, "could not derive a slug from name %q; pass --slug\n", o.name)
		return exitUsage
	}
	o.installDate = boulevard.DateFromTime(time.Now())

	if !o.skipDNS {
		if err := checkDNS(o.baseURL); err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n     Fix the URL, or pass --skip-dns if your DNS isn't live yet.\n", err)
			return exitUsage
		}
	}

	if _, err := os.Stat(o.out); err == nil && !o.force {
		fmt.Fprintf(os.Stderr, "  x  %s already exists. Pass --force to overwrite it.\n", o.out)
		return exitUsage
	}

	ctx := context.Background()

	// Peek at an existing library BEFORE prompting, so a base-URL change is
	// part of what the steward is agreeing to rather than news after the
	// fact. Only open a database that already exists — opening would create
	// the file, and a declined prompt must leave nothing behind.
	if _, err := os.Stat(o.db); err == nil {
		peek, err := store.Open(o.db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return exitIO
		}
		prior, err := peek.LibraryBySlug(ctx, o.slug)
		peek.Close()
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return exitIO
		}
		if err == nil && prior.BaseURL != o.baseURL {
			warnBaseURLChange(os.Stdout, prior.BaseURL, o.baseURL)
		}
	}

	if !o.yes {
		fmt.Printf("\n  Cards will encode:  %s/s/<token>\n  Browse sign:        %s\n\n", o.baseURL, o.baseURL)
		if !confirm(os.Stdin, os.Stdout, "The browse sign is meant to be permanent. Print?") {
			fmt.Println("Nothing written.")
			return exitDeclined
		}
	}

	s, err := store.Open(o.db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	defer s.Close()

	lib, created, err := resolveLibrary(ctx, s, o, rand.Reader, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	toks, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	plan, err := booklet.BuildPlan(booklet.Input{
		Library:   lib,
		Tokens:    toks,
		SourceURL: version.RepoURL,
		BuildLine: "boulevard " + version.Version + " (" + version.Commit + ")",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	pdfBytes, err := booklet.Renderer{CreationDate: time.Now()}.Render(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	if err := os.WriteFile(o.out, pdfBytes, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "  x  write %s: %v\n", o.out, err)
		return exitIO
	}

	verb := "Reprinted"
	if created {
		verb = "Created"
	}
	fmt.Printf("\n  %s %s\n  %s  (%d cards, %s)\n\n  Print it, cut the cards, and scan one before you mount anything.\n\n",
		verb, o.db, o.out, len(toks), lib.Slug)
	return exitOK
}

func checkDNS(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parse %q: %w", baseURL, err)
	}
	host := u.Hostname()
	if _, err := net.LookupHost(host); err != nil {
		return fmt.Errorf("%s does not resolve (%v)", host, err)
	}
	return nil
}

// warnBaseURLChange states plainly what a new base URL costs. DESIGN.md §10
// calls the mounted browse sign the project's real exposure: the digital
// side ejects cleanly, the screwed-to-the-box artifact does not.
func warnBaseURLChange(w io.Writer, from, to string) {
	fmt.Fprintf(w, "\n  !  Base URL is changing from %s to %s.\n"+
		"     Every card and the permanent browse sign will encode the new host.\n"+
		"     The sign already mounted on the box becomes wrong.\n\n", from, to)
}

// confirm asks a yes/no question defaulting to no. The browse sign is meant
// to be permanent, so a bare Enter must never print.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "  %s [y/N] ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

// resolveLibrary loads the library by slug, creating it and minting its
// twelve tokens on first run. It returns whether the library was created.
//
// Existing tokens are NEVER re-minted. Reprinting must reproduce identical
// cards — that is the whole reason secrets are stored in plaintext.
func resolveLibrary(ctx context.Context, s *store.Store, o bookletOpts, entropy io.Reader, warn io.Writer) (boulevard.Library, bool, error) {
	existing, err := s.LibraryBySlug(ctx, o.slug)
	switch {
	case err == nil:
		if existing.BaseURL != o.baseURL {
			warnBaseURLChange(warn, existing.BaseURL, o.baseURL)
		}
		existing.Name = o.name
		existing.LocationLabel = o.location
		existing.BaseURL = o.baseURL
		if err := s.UpdateLibrary(ctx, existing); err != nil {
			return boulevard.Library{}, false, err
		}
		return existing, false, nil

	case errors.Is(err, store.ErrNotFound):
		// fall through to creation

	default:
		return boulevard.Library{}, false, err
	}

	id, err := boulevard.NewLibraryID(entropy)
	if err != nil {
		return boulevard.Library{}, false, err
	}
	lib := boulevard.Library{
		ID: id, Slug: o.slug, Name: o.name,
		LocationLabel: o.location, BaseURL: o.baseURL,
	}
	if err := s.CreateLibrary(ctx, lib); err != nil {
		return boulevard.Library{}, false, err
	}

	periods := tokens.Periods(o.installDate, tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(entropy)
		if err != nil {
			return boulevard.Library{}, false, err
		}
		tokID, err := boulevard.RandomBase32(entropy, boulevard.EntropyBytes)
		if err != nil {
			return boulevard.Library{}, false, err
		}
		toks = append(toks, boulevard.Token{
			ID: tokID, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	if err := s.InsertTokens(ctx, lib.ID, toks); err != nil {
		return boulevard.Library{}, false, err
	}
	return lib, true, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/boulevard/ -v`
Expected: PASS

- [ ] **Step 5: Run the whole suite and vet**

```bash
go vet ./...
go test ./... -v
gofmt -l .
```

Expected: all tests pass, `go vet` silent, `gofmt -l` prints nothing.

- [ ] **Step 6: Exercise the real command**

```bash
cd $(mktemp -d)
go run github.com/gumptionthomas/boulevard/cmd/boulevard booklet \
  --name "The Fairview Boulevard" \
  --location "4th & Fairview, Minneapolis" \
  --base-url https://example.org \
  --out booklet.pdf
```

Confirm: the confirmation prompt appears and a bare Enter aborts with "Nothing written." Re-run with `y` and confirm both files are written. Run it a third time with `--force` and confirm the printed secrets are unchanged (compare with `sqlite3 boulevard.db 'select period_index, secret from tokens order by period_index'`, if sqlite3 is available).

- [ ] **Step 7: Commit**

```bash
git add cmd/boulevard/
git commit -m "feat: boulevard booklet command"
```

---

## Task 12: Manual acceptance and README

**Files:**
- Create: `docs/booklet-acceptance.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the finished binary
- Produces: documentation only

DESIGN.md §12 defines done as *printed on paper, cut with scissors, and a phone scans every card successfully.* That cannot be automated, so it gets a written checklist.

- [ ] **Step 1: Write the acceptance checklist**

Create `docs/booklet-acceptance.md`:

```markdown
# Milestone 0 acceptance

DESIGN.md §12: done means printed, cut with scissors, and every card scans.
Automated tests cannot establish any of this. Run the checklist on real paper
before calling the milestone complete.

## Generate

    boulevard booklet \
      --name "The Fairview Boulevard" \
      --location "4th & Fairview, Minneapolis" \
      --base-url https://your-real-host.example.org

## Print

Household printer, US Letter, **scale set to 100% / "actual size"** — not
"fit to page", which silently shrinks every QR and invalidates the test.

## Check

- [ ] Sheet 1 holds ten cards; sheet 2 holds the cover and two cards.
- [ ] A cut along the hairlines separates the cards cleanly with scissors.
- [ ] One horizontal cut frees the cover intact.
- [ ] A card measures 3.5 x 2 inches and fits a standard business-card sleeve.
- [ ] The purpose bar reads SCAN TO LEAVE OR TAKE and is legible at arm's length.
- [ ] The month is readable at a glance while flipping through a loose stack.
- [ ] Every card shows its own month, dates, and "CARD n OF 12".
- [ ] All twelve QR codes scan from a phone at arm's length in ordinary indoor light.
- [ ] Each card's QR resolves to `<base-url>/s/<something>`, and no two cards
      resolve to the same URL.
- [ ] The browse sign's QR resolves to the bare base URL.
- [ ] Held at driver's-seat distance (~2 m), the card QR is *not* comfortably
      scannable. Presence is the credential; a code readable from a car is a bug.
- [ ] The cover footer names a version, a commit, and the source repository.

## If a card fails to scan

Check print scaling first — it is almost always "fit to page". If scaling was
correct, the base URL may be long enough to push the symbol past the density
guard; the command warns about this, but a warning ignored at generation time
shows up here.
```

- [ ] **Step 2: Replace the README**

Replace `README.md`:

```markdown
# Boulevard

A shelf you have to stand at.

Anyone on the internet may read a Boulevard shelf. Only someone who has
physically stood at the box may change what is on it. Presence is the
credential; there are no accounts.

Named for the strip of grass between sidewalk and street, where Little Free
Libraries stand.

## Status

Milestone 0 — the booklet generator. No server yet.

    boulevard booklet --name "The Fairview Boulevard" \
                      --location "4th & Fairview, Minneapolis" \
                      --base-url https://boulevard.example.org

This writes `boulevard.db` and a printable `boulevard-booklet.pdf`: twelve
tear-off monthly cards, a permanent browse sign, and a cover sheet.

Re-running reprints the *same* cards rather than minting new ones.

## Building

    go build -o boulevard ./cmd/boulevard

To stamp version metadata:

    go build -ldflags "\
      -X github.com/gumptionthomas/boulevard/internal/version.Version=$(git describe --tags --always) \
      -X github.com/gumptionthomas/boulevard/internal/version.Commit=$(git rev-parse --short HEAD)" \
      -o boulevard ./cmd/boulevard

## Documentation

- `DESIGN.md` — the v1 specification and source of truth
- `docs/booklet-acceptance.md` — the manual print-and-scan checklist
- `docs/superpowers/specs/` — per-milestone design documents

## License

AGPL-3.0. See `LICENSE`.
```

- [ ] **Step 3: Run the acceptance checklist**

Print, cut, and scan. Record any failures as issues before declaring the milestone done.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/booklet-acceptance.md
git commit -m "docs: manual acceptance checklist and README"
```

---

## Self-Review Notes

**Spec coverage check:**

| Spec section | Covered by |
|---|---|
| §3 decisions | Tasks 1, 6, 7, 8 (Global Constraints carries the pure-Go SQLite rule) |
| §4 domain types | Tasks 2, 3 |
| §5 package layout, plan/render split | Tasks 8, 9 |
| §6.1 secrets | Task 4 |
| §6.2 periods | Task 5 |
| §6.3 QR payloads and density guard | Tasks 7, 8, 9 |
| §7 storage | Task 6 |
| §8.1 sheet grid | Task 8 |
| §8.2 card face | Task 9 |
| §8.3 browse sign | Task 10 |
| §8.4 cover sheet | Task 10 |
| §9 CLI surface, behavior, exit codes | Task 11 |
| §10 testing | Every task; manual checklist in Task 12 |
| §11 dependencies | Task 1, with API confirmation steps in Tasks 7 and 9 |
| §12 LICENSE, go.mod, ldflags | Task 1 |
| §13 DESIGN.md amendments | Task 1, Step 9 |
| §14 deferred | Out of scope by design |

**Known risks flagged inside the plan:**

- The `skip2/go-qrcode` recovery-level names do not match ECC letters (`High` = ECC Q). Task 7 Step 1 verifies this before any code is written against it.
- The quiet-zone width assumption is verified by a test with a documented fallback.
- The `modernc.org/sqlite` DSN pragma syntax is verified by `TestWALModeIsEnabled`, with instructions to fix the DSN rather than the test.
- Golden-file tests are only created *after* a human looks at the PDF (Task 9 Step 8, Task 10 Step 5). A golden file that locks in a broken layout is worse than none.
