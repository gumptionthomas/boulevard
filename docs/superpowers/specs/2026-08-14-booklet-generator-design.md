# Milestone 0 — The Booklet Generator

**Date:** 2026-08-14
**Status:** design approved, unbuilt
**Scope:** `boulevard booklet`, the standalone command from `DESIGN.md` §8 and §12.

This is the first code in the project. It ships before any server, item model, or web surface.

---

## 1. What this milestone is

A single command that turns four facts about a physical box — its name, its location, its URL, and today's date — into two artifacts:

1. `boulevard.db`, a SQLite file holding one library and its twelve time-gated tokens.
2. `boulevard-booklet.pdf`, a printable US Letter document containing twelve tear-off monthly cards, a permanent browse sign, and a cover sheet.

`DESIGN.md` §12 calls this "the smallest artifact that proves the whole model," and directs that most of the milestone's budget go to the PDF's design rather than the token logic. This spec follows that split: the token logic is specified tightly because it is simple, and the printed artifact is specified in detail because it is the project's only physical surface.

**Done means:** printed on paper, cut with scissors, and a phone scans every card successfully.

## 2. Non-goals

No HTTP server, no scan endpoint, no session, no items, no steward auth, no image handling. Token *validation* (grace windows, activation, revocation) belongs to Milestone 1; this milestone only *generates* tokens and records their periods.

---

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| PDF engine | Pure Go, layout in code | A headless-Chrome dependency contradicts §2's "download one file, run it," which is the stated adoption strategy. The binary must stay self-contained. |
| SQLite driver | `modernc.org/sqlite` (pure Go) | **Hard requirement, not a preference.** A cgo driver forfeits static cross-compiled binaries and breaks the same install promise. |
| Token storage | Real SQLite from day one, `libraries` + `tokens` | §12 permits "database beyond what the tokens need." Honors §10's `library_id` obligation in the first commit and leaves Milestone 1 nothing to migrate. |
| `--base-url` check | Parse + DNS resolve + interactive confirm | §8 demands verification before any PDF is written, but no server exists yet at Milestone 0, so HTTP reachability is unsatisfiable. DNS plus a deliberate pause catches the failure §8 actually fears — a steward who cannot yet answer "what is my URL." |
| Card size | 3.5 × 2 in business card | Sleeves and holders are commodity-available in exactly this size. Smaller also resists drive-by scanning from a car, which protects the presence credential — see §4.1. |
| Sheet grid | 2 × 5 | Ten cards on sheet 1; cover plus the remaining two on sheet 2. |
| Card face | Purpose bar over month-forward body | Month is what the steward must get right monthly; the bar names the verb for visitors and visually separates the card from the browse sign. |
| QR error correction | Level Q (~25%) | A card taped inside a box door through a Minnesota winter gets damp and creased. A steward cannot distinguish a damaged QR from a revoked one, so mid-month failure is the expensive outcome. |

### 3.1 Presence and QR size

The usual instinct — make the QR as large as possible — is wrong here. A code readable from a passing car detaches the scan from the act of standing at the box, which is the entire credential (§1). The QR is therefore sized to be comfortable at arm's length and no larger.

Placement already helps: the card lives inside the box door. But glass-fronted boxes exist, so the small card compounds the protection rather than duplicating it.

---

## 4. Domain types

Package `internal/boulevard` holds pure types with no dependencies.

```go
type LibraryID string   // 128-bit random, Crockford base32 — NOT the slug
type Library struct {
    ID            LibraryID
    Slug          string
    Name          string
    LocationLabel string
    BaseURL       string
}

type Date struct {              // bare calendar date, no timezone
    Year  int
    Month time.Month
    Day   int
}

type TokenState string          // pending | active | expired | revoked
type Token struct {
    ID          string
    LibraryID   LibraryID
    Secret      string
    PeriodIndex int             // 1..12
    ValidFrom   Date
    ValidUntil  Date
    State       TokenState
    FirstSeenAt *time.Time
}
```

`LibraryID` is a distinct type, not a bare string, and it is deliberately **not** the slug — slugs are renameable, IDs must be stable, and §10 requires uniqueness across a host so a database file can be copied out and stand alone.

**Every store method takes a `LibraryID` explicitly. There is no ambient current-library value anywhere in the codebase.** This is §2's "the compiler enforces §10" made concrete, and it is the single most important structural rule in this milestone.

`Date` is a bare calendar date rather than `time.Time`. Period boundaries are calendar facts; carrying a timezone through them invites exactly the offset and DST bugs that §4 rejects TOTP-derived codes to avoid.

---

## 5. Package layout

```
cmd/boulevard/       main.go, booklet.go, version.go
internal/boulevard/  library.go, token.go, date.go
internal/tokens/     secret.go, periods.go
internal/store/      store.go, schema.sql, library.go, token.go
internal/booklet/    plan.go, render.go, card.go, cover.go, sign.go, qr.go, geometry.go
```

### 5.1 The plan/render split

The most important structural choice in the booklet package: **layout planning is separated from PDF drawing.**

```go
type Plan struct { Sheets []Sheet }
type Sheet struct {
    Cards []PlacedCard
    Cover *Cover        // nil on card-only sheets
}
type PlacedCard struct {
    Rect     Rect       // points, origin top-left of page
    Month    string     // "August"
    Year     int
    From     Date
    Until    Date
    Index    int        // 1..12
    Payload  string     // the URL the QR encodes
}
```

`Plan` is pure data computed from inputs, with no PDF library involved. `Render(Plan) → []byte` only draws what the plan describes.

This means the interesting logic — period math, grid placement, which card lands on which sheet — is exhaustively unit-testable without parsing a single PDF, and the rendering layer stays simple enough to verify with one golden file.

---

## 6. Token generation

### 6.1 Secrets

128 bits from `crypto/rand`, encoded with the **Crockford base32 alphabet** (`0123456789ABCDEFGHJKMNPQRSTVWXYZ` — excludes I, L, O and U), uppercase, unpadded. 16 bytes yields 26 characters.

The random source is an injected `io.Reader` defaulting to `crypto/rand.Reader`, so tests can produce deterministic output.

Secrets are stored **in plaintext**, per §4. This is deliberate: a lost booklet is far likelier than server compromise, and reprinting must always reproduce identical cards. Do not hash them.

### 6.2 Periods

A pure function with no I/O:

```go
func Periods(install boulevard.Date, n int) []Period
```

- Period 1 runs from the install date to the last day of that calendar month, however short.
- Periods 2..n are whole calendar months.

A short first period is correct — it aligns every later card to a month boundary, which is what a steward can actually remember.

Cases that must have tests: install on the 1st; install on the 31st; install on the last day of a 30-day month (period 1 is one day long); February in a leap year and in a common year; a period sequence crossing a year boundary; `n = 12` producing exactly twelve non-overlapping, gapless periods.

### 6.3 QR payloads

- Monthly card: `{base_url}/s/{secret}`
- Browse sign: `{base_url}`

At ECC Q with a 26-character secret and a typical host, the payload lands around 58 bytes — QR version 5, 37 × 37 modules. At the specified 83 pt card QR that is ~0.79 mm per module, comfortably scannable.

**Long base URLs degrade this.** The renderer must **refuse** if the payload pushes the symbol above version 8 (49 × 49 modules, ~0.59 mm at the same physical size), where reliability starts to suffer in bad light. Routed to `exitUsage` (2): it is the `--base-url` that is wrong, and nothing is written.

*Amended during implementation.* This originally said "warn". A warning ships a booklet whose cards may not scan, and a steward cannot tell a marginal QR from a damaged or revoked one — the expensive failure §3 already names. An unscannable card that ships is worse than a refused generation.

---

## 7. Storage

```sql
CREATE TABLE libraries (
    id             TEXT PRIMARY KEY,
    slug           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    location_label TEXT NOT NULL,
    base_url       TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE tokens (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id),
    secret        TEXT NOT NULL UNIQUE,
    period_index  INTEGER NOT NULL,
    valid_from    TEXT NOT NULL,        -- YYYY-MM-DD
    valid_until   TEXT NOT NULL,        -- YYYY-MM-DD
    state         TEXT NOT NULL,
    first_seen_at TEXT,
    created_at    TEXT NOT NULL,
    UNIQUE (library_id, period_index)
);
```

`secret` is unique **host-wide**, not per library — §10 requires this so a token identifies its library unambiguously.

Columns are a superset of the domain structs in §4: `created_at` is bookkeeping and has no domain field. The store maps rows to structs explicitly rather than reflecting over them.

Connection setup: `journal_mode=WAL`, `foreign_keys=ON`, a busy timeout, and `SetMaxOpenConns(1)` on the write pool. Milestone 0 is a single short-lived process that does not strictly need this, but establishing the pattern here costs nothing and §2 names `SQLITE_BUSY` as a known hazard.

The bounded LRU cache of per-library handles (§10) is **not** built in this milestone — there is one library and one process. It belongs with the host layer.

---

## 8. Printed geometry

All dimensions in PostScript points (72 pt = 1 in). US Letter is 612 × 792 pt.

### 8.1 Sheet grid

| Quantity | Value |
|---|---|
| Card | 252 × 144 pt (3.5 × 2 in) |
| Grid | 2 columns × 5 rows = 504 × 720 pt |
| Horizontal margin | 54 pt (0.75 in) each side |
| Vertical margin | 36 pt (0.5 in) each side |

- **Sheet 1** — cards 1–10, filling the grid.
- **Sheet 2** — the cover occupies rows 1–4 (504 × 576 pt at origin 54, 36); cards 11 and 12 sit in row 5 (y 612–756).

Sheet 2 therefore cuts as one horizontal cut across the page at y = 612, then one vertical cut through the bottom strip. The cover survives intact as a 7.5 × 8 in page.

Cards are edge-to-edge with shared cut edges. Draw hairline rules on the grid plus tick marks extending into the margins, so a steward can align a straightedge. Assume scissors, not a guillotine (§4).

### 8.2 Card face

Within each 252 × 144 pt card:

- **Purpose bar** — full width, 19 pt tall, filled `#14130D`, reversed type. `SCAN TO LEAVE OR TAKE`, 7.5 pt bold sans, letterspaced, centered.
- **Body** — remaining 125 pt, padded 13 pt horizontally.
  - **QR** — 83 pt square (1.15 in), left, vertically centered in the body. Drawn as **vector rectangles from the module bitmap**, never an embedded raster — the PDF stays resolution-independent and prints crisply at any scale.
  - **Text column** — begins 107.5 pt from the card's left edge:
    - Month name, 15 pt serif semibold (`August`)
    - Year, 9.5 pt serif at 60% gray
    - Hairline rule
    - Period dates, 7 pt sans at 75% (`Aug 14 – Aug 31`)
    - `CARD 1 OF 12`, 6.5 pt sans uppercase letterspaced at 55%

The card index makes a cut stack self-ordering, which matters once twelve loose cards are sitting in a drawer.

### 8.3 Browse sign

A 302.4 × 158.4 pt (4.2 × 2.2 in) cut-out on the cover sheet, bordered with cut lines. Light, landscape, undated — deliberately unlike the monthly card.

- Library name, 14 pt serif semibold
- `Scan to browse the shelf`, 8 pt sans bold
- `Anyone, anywhere, anytime. No code needed.`, 7 pt sans at 70%
- QR at 108 pt (1.5 in), encoding the bare base URL

This is the artifact §10 calls the permanent exposure. The cover sheet must state that it belongs **in a sleeve, not laminated or engraved**, so it stays replaceable when a hosting arrangement ends.

**The library name has a budget.** The text column runs from the QR to the sign's right border — 154.4 pt — and the name gets at most two lines of 14 pt Times Bold there, which is roughly 35 characters of ordinary mixed case. A longer name is **refused**, not drawn past the border: this sign is mounted in public and read by strangers, and a steward must learn the name does not fit at generation time rather than after screwing it to the box. The refusal is a `--name` validation error routed to `exitUsage` (2), raised before the DNS check and before any database write, and it reports the measured budget for the name given. The same check remains inside the renderer as a backstop.

*Amended during implementation.* The original spec never contemplated a name too long for the sign; an ordinary one — "Highland Park Neighborhood Little Free Library" — exceeds it.

### 8.4 Cover sheet

Occupies rows 1–4 of sheet 2, containing in order: a title block naming the library and its location; the browse sign cut-out; install instructions; the signage line; and a footer carrying the source URL, version, and commit.

Install instructions must cover: cut the cards, keep them in order, tape the current month inside the box door, swap on the first of each month, and mount the browse sign in a sleeve.

---

## 9. CLI surface

```
boulevard booklet --name --location --base-url [--slug] [--out] [--db]
                  [--skip-dns] [--yes] [--force]
boulevard version
```

| Flag | Default | Notes |
|---|---|---|
| `--name` | required | Library display name |
| `--location` | required | Human location label |
| `--base-url` | required | Scheme + host, no path or query |
| `--slug` | derived from name | Lowercase, spaces to hyphens, non-alphanumerics stripped |
| `--out` | `boulevard-booklet.pdf` | |
| `--db` | `boulevard.db` | |
| `--skip-dns` | off | For a domain not yet live |
| `--yes` / `-y` | off | Skip the confirmation prompt |
| `--force` | off | Overwrite an existing output file |

### 9.1 Behavior

1. Validate the URL shape. Reject a path, query, or fragment — every card encodes this host. The host is lowercased: it is compared literally on every re-run, and case alone must not read as a base-URL change.
2. Validate that `--name` fits the browse sign (§8.3).
3. Resolve the hostname via DNS unless `--skip-dns`. On failure, refuse and name the host.
4. Refuse to overwrite an existing `--out` without `--force`.
5. Print what this run will do — reprint an existing library, create a new one, or repair one holding no cards — naming the library, its slug, and what will be encoded. When a new library is about to join a database that already holds others, list them: the slug derives from `--name`, so a typo mints a second library rather than reprinting the first. Then require a `y/N` confirmation unless `--yes`.
6. Open or create the database, owner-readable only: it holds token secrets in plaintext, and a secret is the write credential for the shelf. The library is looked up **by slug**. If it already exists, **reprint its existing secrets rather than minting new ones** — this is the entire reason §4 stores them in plaintext. If it exists but holds no tokens — the wreckage of a run interrupted between the two transactions — mint its booklet rather than failing forever.
7. Reconcile the library record. `--name` and `--location` overwrite the stored values silently. `--base-url` also overwrites, but only after warning loudly, and exactly once, that every card and the permanent browse sign now encode a different host; §8 explicitly permits regeneration after a base-URL change. Tokens are never re-minted by any of this — only the printed URLs change.
8. Write the PDF, owner-readable only: every card in it carries a secret.

*Amended during implementation.* The `--out` existence check was step 6, after the confirmation. It runs before the prompt instead — failing fast beats failing after asking. The steps are renumbered to match, and the pre-flight `--name` check (§8.3) and the file modes are recorded here.

### 9.2 Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Confirmation declined — nothing written |
| 2 | Usage or validation error (bad URL, DNS failure, output exists, `--name` too long for the browse sign, base URL too long for a comfortable QR) |
| 3 | I/O or database error |

---

## 10. Testing

**Unit — `internal/tokens`.** Table-driven period math covering every case in §6.2. Secret encoding: alphabet membership, length, and determinism under an injected random source.

**Unit — `internal/booklet` plan.** Every card rect falls inside the printable area; sheet assignment is 10 + 2; all twelve secrets appear exactly once across the plan; the cover appears on exactly one sheet.

**Unit — `internal/store`.** Round-trip a library and its tokens; re-running generation returns identical secrets; the host-wide unique constraint on `secret` rejects a duplicate.

**Golden file — render.** One PDF byte-compared against `testdata/`, with a `-update` flag. Determinism requires injecting the random source, the install date, and the PDF creation date — all three must be parameters, not ambient calls.

**Manual acceptance (required by §12, not automatable).** Print sheet 1 and sheet 2 on a household printer. Cut with scissors. Confirm: every card cuts cleanly on its lines; all twelve QR codes scan from a phone at arm's length in ordinary indoor light; the card fits a standard business-card sleeve; the browse sign cuts out intact; the month on each card is legible at a glance from a stack.

---

## 11. Dependencies

| Purpose | Package | Constraint |
|---|---|---|
| PDF | `github.com/go-pdf/fpdf` | Maintained fork of gofpdf |
| QR | `github.com/skip2/go-qrcode` | Must expose ECC level selection and a module bitmap |
| SQLite | `modernc.org/sqlite` | **Pure Go — no cgo** |

Pin exact versions and confirm maintenance status at implementation time. The QR choice is replaceable; what matters is bitmap access for vector drawing and ECC level control. The pure-Go SQLite constraint is not replaceable.

All three must be AGPLv3-compatible (§2): MIT, BSD, or Apache-2.0.

---

## 12. Also lands in this milestone

- `LICENSE` — AGPL-3.0.
- `go.mod` at module path `github.com/gumptionthomas/boulevard`.
- Version, commit, and repository URL embedded via `-ldflags`, surfaced by `boulevard version` and printed in the cover sheet footer. §2 calls for compliance by construction; it is cheapest in the first commit.

---

## 13. Amendments to DESIGN.md

Two changes, both arising from the observation that a visitor meeting two unlabeled QR codes cannot tell them apart:

1. **The card's verb becomes "Scan to leave or take."** §4's signage line reads "Scan to leave," but §5 gates *taking* behind a session identically. A visitor who reads only the original line will not scan, will tap take, and will find the control inert. The mounted sign keeps its existing line; the card names both verbs.
2. **The browse sign gains explanatory copy** — "Scan to browse the shelf / Anyone, anywhere, anytime. No code needed." This states the thesis (§1) in the one place a confused visitor is physically standing.

Together these make each code name its own verb, and the two artifacts deliberately do not resemble each other: browse is mounted, light, landscape, undated; leave/take is inside the door, dark-barred, dated, business-card sized.

---

## 14. Deferred to Milestone 1

Token validation with the seven-day grace window, first-scan activation, expiry of the prior active token, steward overrides (force-activate, extend, revoke), the `/s/:token` endpoint, and session cookies.

**A second year's booklet.** §9.1 step 6 reprints an existing library's secrets rather than minting new ones, which is what makes a lost booklet reprintable. It also means that once the twelve months elapse, re-running reprints the same twelve now-expired cards, forever: there is no path to year two. This is deliberate for Milestone 0 — a booklet is generated once, and expiry has no meaning until token validation exists — but it needs a command of its own (a `--renew`, or an explicit next-period mint) alongside the validation work, or it will be rediscovered as a bug the first time a steward reaches month thirteen.
