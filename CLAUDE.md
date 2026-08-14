# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository state

Greenfield. `DESIGN.md` is a locked v1 specification; no implementation exists yet (the repo contains only `README.md` and `DESIGN.md`). There is no `go.mod`, no build, no test suite.

**`DESIGN.md` is the source of truth.** Read the relevant section before implementing anything. It records not just decisions but the reasoning behind them, and several of its rules are counterintuitive enough that an agent will "improve" them by accident — see Invariants below.

## Stack (decided, §2 of DESIGN.md)

Go + SQLite, single static binary. AGPL-3.0, no CLA.

Two rationales drive code style: the binary must stay a one-file download (adoption strategy), and this is a largely agent-written codebase, so the type system is the review budget. Prefer a distinct `LibraryID` type and a repository layer where every method takes one, over any ambient "current library" value.

SQLite operational requirements: WAL mode; `SetMaxOpenConns(1)` on the write pool (concurrent takes otherwise produce `SQLITE_BUSY`); a bounded LRU cache of per-library DB handles, never one per request.

## Commands

None exist yet. Once `go.mod` is created, standard Go tooling applies (`go build ./...`, `go test ./...`, `go test -run TestName ./pkg`).

The CLI surface the spec targets (§8) — build toward these signatures:

```
boulevard booklet --name "..." --location "..." --base-url https://... --out booklet.pdf
boulevard init --name "..." --location "..." --base-url https://...
boulevard version
```

## Build order (§12)

Milestone 0 is `boulevard booklet` standalone — twelve time-gated tokens, base-URL verification, printable PDF — before any server, database, or item model. Done means printed, cut with scissors, and every card scans from a phone. Spend the milestone's budget on the PDF's design, not the token logic.

Then: 1 presence (scan → session cookie) · 2 shelf (items, leave form, approval queue) · 3 mechanics (copies, take, FIFO eviction, expiry, shed) · 4 steward admin · 5 polish · 6 (v2) host layer.

## Architecture

**Thesis: consumption is global, mutation is local.** Anyone may read a shelf. Only someone who scanned the rotating code at the physical box may change it. Presence is the credential; there are no passerby accounts. Every design decision follows from this — if a feature request violates it, the answer is no.

**Take is a write.** It decrements a finite shelf, so it is gated identically to leave. Do not treat it as a read-side action.

**Two codes, one physical artifact.** A stable *browse* QR is mounted permanently and encodes the host root. A rotating monthly *leave/take* QR comes from a printed 12-card booklet and encodes `/s/<token>`. The booklet is a permanent part of the design, not a placeholder for hardware.

**Multi-tenancy is architected in v1, shipped in v2 (§10).** v1 ships one library, but nothing may assume a singleton: `library_id` is a first-class key on every item, token, session, and setting; every query is library-scoped; canonical routes are `/b/:slug/...` (single-library mode may redirect `/`); token secrets are unique host-wide, not per library. v2 storage is one SQLite file per library plus a registry DB — which is what makes ejection a file copy rather than an export format.

## Invariants — do not "fix" these

Each of these looks like an oversight and is not. The reasoning is in `DESIGN.md`.

- **No host-wide public feed** ("recent across all shelves"). Four lines of code, would be the most-visited page, and is the algorithmic feed entering through the service entrance. A map sends you to a place; a feed brings places to you.
- **`views` drives nothing.** Views and takes are separate counters and both are displayed, but only takes affect shelf state. Attention-weighted eviction rebuilds the feed this project reacts against.
- **Eviction is FIFO on the oldest non-pinned item.** Deliberately dumb, deliberately not popularity-aware.
- **Token periods are explicit stored calendar dates, never TOTP-derived.** Clock drift, DST, and a steward swapping the card late must be inspectable and fixable, not silent auth failures. Validation carries a **7-day grace** on both ends; out-of-window-but-valid renders a distinct "card is out of date" page, not the generic failure page.
- **Token secrets are stored in plaintext.** Deliberate: a lost booklet is far likelier than server compromise, and reprinting must always work. Do not hash them.
- **GPS/lat/lng is display-only and never an auth factor.**
- **`--base-url` is required and verified before any PDF is written.** A wrong browse sign is the one permanent artifact.
- **`note` on an item is required.** The note is the point; the media is the excuse.
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
