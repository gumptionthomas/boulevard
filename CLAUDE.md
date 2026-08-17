# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository state

Milestones 0 through 4a are built: `boulevard booklet` prints the twelve-card booklet and the browse sign, `boulevard serve` runs the shelf, `boulevard queue` / `approve` / `reject` run the approval CLI, `boulevard shed` / `reshelve` / `release` run the shed, and `boulevard steward-key` mints the steward's credential. Scanning a card grants a 24-hour session; a session can leave an item, which waits `pending` until a steward approves it onto the shelf or rejects it, and can take an item, which is undoable until the session ends. Items shed by eviction, expiry, being taken to zero copies, or a steward's removal wait for a steward to re-shelve or release them. The steward's desk — login, hub, queue, shelf, shed, settings, pins — is reachable from a phone at `/b/{slug}/steward/`; the CLIs remain for headless use. Token management (force-activate, extend, revoke, new booklet) and export are not built yet — that is Milestone 4b.

```
cmd/boulevard/     main.go booklet.go serve.go queue.go shed.go stewardkey.go version.go
internal/booklet/  the PDF: cards, cover, QR, geometry, layout
internal/boulevard/ domain types only, stdlib-only: Date, Library, Item, Token, Session, Steward, ids
internal/store/    SQLite: schema.sql, migrate.go, library.go, item.go, token.go, session.go, take.go, shed.go, sweep.go, steward.go
internal/tokens/   pure: periods, secrets, Validate
internal/web/      HTTP: server, routes, scan, shelf, leave, item, take, steward, steward_items, steward_settings, render, logging, templates/
internal/version/  version, commit, repo URL for the AGPL footer
```

**`DESIGN.md` is the source of truth.** Read the relevant section before implementing anything. It records not just decisions but the reasoning behind them, and several of its rules are counterintuitive enough that an agent will "improve" them by accident — see Invariants below. Per-milestone designs live in `docs/superpowers/specs/`, and where one deviates from `DESIGN.md` it says so and `DESIGN.md` is amended to match.

## Stack (decided, §2 of DESIGN.md)

Go + SQLite, single static binary. AGPL-3.0, no CLA.

Two rationales drive code style: the binary must stay a one-file download (adoption strategy), and this is a largely agent-written codebase, so the type system is the review budget. Distinct `LibraryID`, `SessionID` and `Date` types over bare strings and `time.Time`, and an explicit identifier over any ambient "current library" value.

Dependencies have to earn their place against the single-binary promise. Routing is `net/http`'s own pattern matching, templates are `html/template` behind `go:embed` with the CSS inlined into the layout, and the SQLite driver is pure-Go (`modernc.org/sqlite`) because cgo would break the static build.

SQLite operational requirements: WAL mode; `SetMaxOpenConns(1)` (concurrent writers otherwise produce `SQLITE_BUSY`), which also means every request serializes through one connection, so no handler may block; v2 adds a bounded LRU cache of per-library DB handles, never one per request.

## Commands

```
go build -o boulevard ./cmd/boulevard
go test ./...            # every package; the suite is fast, run all of it
go test -run TestName ./internal/web
go vet ./... && gofmt -l .   # both must be clean before committing
TZ=America/Chicago go test ./...   # see "injected clocks" below
```

The CLI as it stands (`DESIGN.md` §8; `init` is not built yet):

```
boulevard booklet --name "..." --location "..." --base-url https://... [--out booklet.pdf] [--db boulevard.db]
boulevard serve   [--db boulevard.db] [--addr :8080]
boulevard queue    [--db boulevard.db] [--slug SLUG]
boulevard approve  [--db boulevard.db] [--slug SLUG] <id>
boulevard reject   [--db boulevard.db] [--slug SLUG] <id>
boulevard shed        [--db boulevard.db] [--slug SLUG]
boulevard reshelve    [--db boulevard.db] [--slug SLUG] <id>
boulevard release     [--db boulevard.db] [--slug SLUG] <id>
boulevard steward-key [--db boulevard.db] [--slug SLUG]
boulevard version
```

`serve` speaks plain HTTP and terminates no TLS. An `https://` base URL needs a reverse proxy in front of it, and the base URL is baked into twelve printed cards, so getting it wrong means reprinting.

## Build order (§13)

0 booklet ✅ · 1 presence ✅ (scan → session cookie) · 2 shelf ✅ (items, leave form, approval queue) · 3 mechanics ✅ (take, undo, session sweeping, expiry, the shed, rate limits) · 4a steward's desk ✅ (generated key, the admin shell at `/b/{slug}/steward/`, queue/shelf/shed/settings pages, pins, remove-sheds) · **4b token management and export next** (force-activate, extend, revoke, new booklet) · 5 polish · 6 (v2) host layer.

**Milestone 4 split in two.** `DESIGN.md` §13 names one Milestone 4; the spec that built it (`docs/superpowers/specs/2026-08-17-steward-desk-design.md`) split it into 4a (this: auth, hub, queue, shelf, shed, settings — everything about what is on the shelf) and 4b (token management and export — everything about the box and its data), because bundled they were larger than Milestone 3, which already ran to nine tasks and a whole-branch review. The two share only the admin shell.

**Section numbers shifted on 15 Aug 2026.** `DESIGN.md` §11 became pop-up boulevards, pushing Later, Build order and Open to §12, §13 and §14. Specs and plans under `docs/superpowers/` predate that and cite the old numbers — in those files, "§12" means build order and "§11" means Later. They are dated records of what was decided at the time, so they were left as written rather than rewritten to match.

Milestone 1 honors a `revoked` token state it never sets, and enforces no rate limits — both wait for the milestones that own them. Milestone 2 adds items to a shelf page that already existed rather than building the page and the items together, and approval is a CLI (`queue`/`approve`/`reject`), not a web admin — that is Milestone 4a. Milestone 3 built copies, take, and FIFO eviction on top of what Milestone 2 already shipped in the approval transaction, added session sweeping as a correction to Milestone 1 (see Invariants below), and gave the shed the approval queue's CLI shape rather than a web admin — that is still Milestone 4a, and both CLIs stay even now that the web admin exists. Milestone 4a made `ApproveItem`'s all-pinned refusal reachable: the comment describing it, and the sentinel it now returns, predate this milestone by name but not by behavior — nothing set `pinned` before pins existed to set.

## Architecture

**Thesis: consumption is global, mutation is local.** Anyone may read a shelf. Only someone who scanned the rotating code at the physical box may change it. Presence is the credential; there are no passerby accounts. Every design decision follows from this — if a feature request violates it, the answer is no.

**Take is a write.** It decrements a finite shelf, so it is gated identically to leave. Do not treat it as a read-side action. It is also `POST`-only: a `GET` take URL would be shareable, prefetchable, and — because it redirects to the item's own payload — an open redirect wearing the shelf's domain.

**Two codes, one physical artifact.** A stable *browse* QR is mounted permanently and encodes the host root. A rotating monthly *leave/take* QR comes from a printed 12-card booklet and encodes `/s/<token>`. The booklet is a permanent part of the design, not a placeholder for hardware.

**Multi-tenancy is architected in v1, shipped in v2 (§10).** v1 ships one library, but nothing may assume a singleton: `library_id` is a first-class key on every item, token, session, and setting; every query is library-scoped; canonical routes are `/b/:slug/...` (single-library mode may redirect `/`); token secrets are unique host-wide, not per library. v2 storage is one SQLite file per library plus a registry DB — which is what makes ejection a file copy rather than an export format.

**What Milestone 1 settled, and why it looks odd.** The session cookie is an opaque 128-bit random id and is **not signed**: the server-side row is the source of truth, so a signature would add a key to store, rotate and lose while preventing no forgery the lookup does not already reject — and it would break §10's promise that ejecting a library is a file copy. Session expiry is checked on read; Milestone 1 shipped no sweeper for a table holding a handful of rows, and Milestone 3 corrected that once a second feature needed sessions to actually go away (see Invariants below) — the sweep still runs on read paths, never a background goroutine. Token activation moves **forward only**: an older card in its grace window still grants a session and still records `first_seen_at`, but must not rewind which card the steward thinks is in the door. HTML responses are `Cache-Control: no-store` plus `Vary: Cookie`, because the shelf's body differs entirely depending on the session cookie and this branch ships no TLS, so a reverse proxy or CDN is likely in front of it. Rationale for all four: `docs/superpowers/specs/2026-08-14-presence-design.md`.

## Invariants — do not "fix" these

Each of these looks like an oversight and is not. The reasoning is in `DESIGN.md`.

- **No host-wide public feed** ("recent across all shelves"). Four lines of code, would be the most-visited page, and is the algorithmic feed entering through the service entrance. A map sends you to a place; a feed brings places to you.
- **`views` drives nothing.** Views and takes are separate counters. Takes are displayed on the item page (§7); views are recorded but not yet displayed anywhere — that is a §7 matter this milestone did not scope, not a gap to close here. Regardless of whether views are ever shown, only takes may affect shelf state. Attention-weighted eviction rebuilds the feed this project reacts against.
- **Eviction is FIFO on the oldest non-pinned item.** Deliberately dumb, deliberately not popularity-aware.
- **Token periods are explicit stored calendar dates, never TOTP-derived.** Clock drift, DST, and a steward swapping the card late must be inspectable and fixable, not silent auth failures. Validation carries a **7-day grace** on both ends. Outside it, a valid card gets a diagnostic page rather than the generic failure page — and *which* one depends on the side: `Expired` says the card is out of date and names the day it stopped, `NotYet` says it is not in use yet and names the day it starts. Do not merge them back into one outcome; the copy is wrong for one of the two. A second reason this must stay explicit rather than derived: §11's pop-up boulevards run periods of hours or days, so nothing may assume a period is a calendar month. Widening `Date` to a timestamp is a known future migration; deriving the boundary from a clock function is not.
- **Token secrets are stored in plaintext.** Deliberate: a lost booklet is far likelier than server compromise, and reprinting must always work. Do not hash them. It follows that a secret **is** the shelf's write credential: `boulevard.db` and the PDF are written `0600`, and nothing may log a scan URL's path, put a secret in an error message, or copy the database anywhere loose.
- **The steward key is generated, never chosen, and only its SHA-256 hash is stored.** This is not the same decision as the plaintext token secrets just above, and the two are not in tension: a token secret must stay recoverable because reprinting a lost booklet must always work, while a steward key has no artifact to reprint — losing it just means running `boulevard steward-key` again. There is nothing worth keeping recoverable, so it is not kept. Every steward route 404s, not 403s, until a key exists — a fresh install should not advertise a surface that is not armed.
- **Steward sessions live in `steward_sessions`, a table of their own, not `sessions`.** Milestone 3's presence sweep deletes every expired row in `sessions` unconditionally, and that sweep's whole privacy argument depends on having no exceptions. A steward session stored alongside presence sessions would either get swept out from under a logged-in steward or force the sweep to grow a carve-out — and a carve-out in a privacy mechanism is how the mechanism stops being trustworthy.
- **`steward_key_hash` is deliberately not a field on `boulevard.Library`.** `UpdateLibrary` takes a whole `Library` and writes it back read-modify-write style (the settings form is the reason); if the hash lived on the struct, a handler that built one from form values alone would silently blank the one credential whose loss locks the steward out of their own box. Keeping it off the struct means no amount of careless struct-building can reach it — it is read and written only by the dedicated methods in `internal/store/steward.go`.
- **Unknown and revoked secrets must be byte-identical responses.** Same status, same body — `TestScanRevokedIsIndistinguishableFromUnknown` compares them directly. Anything library-specific on that page confirms to whoever photographed a card that its secret was real. This forces the generic failure page to know nothing at all: no name, no location, no shelf link. Once a token has resolved and validated, the constraint is spent — from there, a database failure is a 500 and says so.
- **No store method infers a current library.** Resolution boundaries (`LibraryBySlug`, `LibraryByID`, `TokenBySecret`, `SessionByID`, `DeleteSession`) are handed an identifier and return what it names, so they take no `LibraryID`. Host-scoped queries (`LibrarySlugs`) take none because the question is about the host. Everything else takes an explicit `boulevard.LibraryID`. Nothing anywhere falls back to "the only library".
- **Clocks are injected, never read in place.** `web.New` takes a `now func() time.Time` and `tokens.Validate` takes `today` as a parameter, so grace boundaries can be tested on the exact day. Two related traps: the store round-trips timestamps through RFC3339 **in UTC**, so anything formatting one must convert into the display zone first (the banner is server-local, spec §10.2); and a test that pins UTC on both sides of a time comparison will pass while the real thing is a day out. Use a non-UTC clock in any test that formats a time.
- **GPS/lat/lng is display-only and never an auth factor.**
- **`--base-url` is required and verified before any PDF is written.** A wrong browse sign is the one permanent artifact.
- **`note` on an item is required.** The note is the point; the media is the excuse.
- **Sessions are swept, lazily, on the shelf and item read paths, plus once at `serve` startup — not by a background goroutine.** Expiry is still checked on read regardless; sweeping and the read check are belt and braces, not alternatives. This bounds any session-scoped link to the session's 24 hours **only under traffic**: the read-path sweep needs someone to load the shelf, and DESIGN.md itself says a quiet box is the common case, not the exception. The startup sweep is a cheap belt for that — it bounds the link on a restart even with zero traffic — but it is not a substitute for the qualifier: between restarts, on a box nobody visits, an expired session's take rows outlive 24 hours for as long as the session row does. What makes an undoable take possible is the sweep eventually running, not a hard 24-hour ceiling.
- **A left item still stores no session reference, even though sessions are now swept.** The asymmetry with take is deliberate, not an oversight: a left item is public and permanent, so a link from it would point at durable data from the other side and survive the sweep — everything one person left, kept forever, the user record §1 forbids. A take is a private act against a shelf, so it links (in `session_takes`) and that row is deleted with the session, which keeps it well under 24 hours old and out of user-record territory. Per-session leave limits are still counted on a bare counter on the session row instead. This also means a revoked card's *left* items still cannot be retracted, which is the accepted cost.
- **The request log never carries a remote address or a session id.** `logRequests` (`internal/web/logging.go`) logs the method, redacted path, status and duration — item ids included — and stops there on purpose. Add a remote address or a session id to that line and `POST /b/x/i/ABC/take` becomes, one log line at a time, exactly the durable "who took what" record `session_takes`'s 24-hour, swept-with-the-session design exists to avoid being — logs are not swept, do not expire, and are the first thing that ends up in a support thread or a backup. "Log the client IP so the steward can debug" is the natural-sounding request that would rebuild it silently; the answer is no, for the same reason a left item stores no session reference.
- **Nothing fetches a submitted URL.** No titles, no thumbnails, no embeds, no oEmbed. An outbound request per stranger submission is an SSRF surface and a request amplifier, and a hostile URL could borrow a trustworthy title. The shelf shows a domain.
- **Video is never hosted** — a YouTube/Vimeo/PeerTube URL is a `link` that renders an embed.
- **Steward additions go through the same presence flow**, even though the steward owns the server.
- **`slots` never auto-scales with traffic.** Only the steward changes it.
- **No e-ink display for the rotating code.** Rejected on the merits, not deferred: swapping the paper card is the only thing that reliably walks a steward to their box monthly.
- **Images: strip EXIF including GPS**, resize, cap dimensions and file size, on upload.
- **AGPL compliance by construction:** source link in every public page footer; version, commit, and repo URL embedded in the binary and surfaced on `/about` and `boulevard version`; modified builds require a source URL at install.

## Design constraints on the UI

The shelf page is used one-handed, on a phone, outdoors, in bad light, possibly in a Minnesota winter. Large tap targets, high contrast, no hover states, fast on cold cellular.

Without a session, leave/take controls are **visible but inert** with an explanation — never hidden. A remote reader should understand the rule, not think the feature is missing.

The **empty state ships as-is and gets real design attention**; it is arguably the most important screen. An empty shelf is an invitation, and is better than one stale link.

Avoid mechanics that generate steward labor — that is the path to the steward-blog failure mode.

## v1 non-goals (§9)

Donations · federation · accounts, profiles, follows, notifications, comments, likes · hosted multi-tenancy · hosted video · full-text search · native apps.
