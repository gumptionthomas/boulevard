# Boulevard — v1 Specification

**Working name.** Named for the strip of grass between sidewalk and street, where Little Free Libraries stand.

**Status:** design locked, unbuilt. This document is the source of truth for v1.

---

## 1. Thesis

**Consumption is global. Mutation is local.**

Anyone on the internet may read a Boulevard shelf, follow its links, watch its videos, and share its URL. Only someone who has physically stood at the box may change what is on the shelf — whether by leaving something or by taking it.

Presence is the credential. There are no accounts.

### Why this is the whole product

Digital sharing has zero marginal cost, so any public "add" endpoint becomes a spam target the moment it is interesting. Physical presence restores the scarcity that makes a Little Free Library work, and it does so without moderation labor, identity, or an algorithm.

Every design decision below follows from the thesis. If a feature request violates it, the answer is no.

---

## 2. Decide before writing code

**Stack: Go + SQLite, single static binary.** DECIDED.

Rationale:

- **Install is the adoption strategy.** Download one file, run it. No runtime, no dependency hell, trivial to support a stranger over email.
- **The type system enforces §10.** The bug that will actually hurt Boulevard is a missing library scope — a query returning another boulevard's items, or a token check that skips library verification. Use a distinct `LibraryID` type and a repository layer where every method takes one. No ambient "current library" anywhere. The compiler then enforces the v1 multi-tenancy obligation instead of the author remembering to.
- **Explicit code is auditable code.** This project is largely agent-written; generated code is fast to produce and slow to review. A compiler that rejects whole categories of mistake before review is worth more here than on a hand-typed project. Go's verbosity is an asset under these conditions.
- **Multi-tenancy ops.** Background sweeps across N libraries and concurrent access to many database files, in one binary the host runs.

Known costs, accepted:

- **Imaging and PDF are weaker than Python's.** No true equivalent to Pillow or reportlab. Use `go-pdf/fpdf` for the booklet, or render it as HTML via headless Chrome; `disintegration/imaging` plus a separate EXIF library for uploads. Milestone 0 is exactly PDF and QR generation, so this is felt immediately.
- **SQLite concurrency needs care.** Enable WAL mode. Hold writes to a single connection (`SetMaxOpenConns(1)` on the write pool) or concurrent takes will produce `SQLITE_BUSY`. With per-library database files (§10), maintain a bounded LRU cache of open handles — never one per request.

**License: AGPL-3.0.** DECIDED.

The sharing ethos overrides the convenience of permissive licensing. AGPL's network clause covers software users interact with without receiving a copy — precisely the hosted multi-tenant case in §10. Without it, a hosting company could run a closed, improved Boulevard for a hundred neighborhoods and share nothing back.

Consequences, understood and accepted:

- **Commercial use is permitted.** Anyone may sell Boulevard hosting. They simply cannot do it on a closed fork, and stewards retain their exit rights (§10).
- **No CLA.** Contributors keep their copyright, which makes relicensing practically impossible later. Chosen deliberately: a CLA on a neighborhood project signals future commercialization and adds friction to first-time contributors. AGPL is therefore permanent.
- **Dependency licenses must be AGPLv3-compatible.** MIT, BSD, and Apache-2.0 are fine. Avoid GPLv2-only dependencies.
- **The name is not covered by the license.** Trademark and identity are separate; see §14.

**Compliance by construction.** Hosts are neighbors, not lawyers, and AGPL places the source-offer obligation on whoever runs a modified version. Build compliance in so that a host who never thinks about licensing is compliant regardless:

- Every public page footer carries a "source" link.
- The binary embeds its own version, commit, and repository URL, surfaced on `/about` and via `boulevard version`.
- Hosts running a modified build set a source URL at install; the field is required, not optional.

---

## 3. Domain model

### Library

A boulevard is bound to one physical box. v1 ships a single-library experience, but **nothing in the code may assume a singleton** — see §10.

| Field | Notes |
|---|---|
| `slug` | URL-safe. `fairview`. Unique per host. |
| `name` | e.g. "The Fairview Boulevard" |
| `location_label` | Human text. "4th & Fairview, Minneapolis" |
| `lat` / `lng` | Optional, display only. **Never used for auth.** |
| `slots` | Shelf capacity. Default 12. |
| `max_age_days` | Default 90. |
| `default_copies` | Default 3. |
| `steward_contact` | Optional, shown on the about page. |

GPS is never a factor in authentication: spoofable, fails indoors, and forces a permission prompt on a stranger in the first five seconds.

### Item

| Field | Notes |
|---|---|
| `type` | `link` \| `text` \| `image` |
| `payload` | URL, body text, or stored image path |
| `note` | **Required.** Why the leaver left it. |
| `attribution` | Optional freeform string. "the guy with the beagle" |
| `copies_total` / `copies_left` | |
| `state` | `pending` \| `shelved` \| `shed` \| `released` |
| `pinned` | Steward only, max 3 |
| `left_at` / `shelved_at` | |
| `views` / `takes` | Separate counters. See §7. |
| `shed_at` | Set when the item enters the shed. |
| `shed_reason` | `evicted` \| `expired` \| `taken` — how it got there. See §5. |

An item is `pending` from the moment it is left until the steward approves it (`shelved`) or rejects it (`released`). §5 requires an approval queue, and a queue is a state.

**Video is never hosted.** A YouTube/Vimeo/PeerTube URL is a `link` that renders with an embed.

The `note` is mandatory and is the point. The media is the excuse.

### Presence session

Ephemeral. Created by scanning the rotating code, expires 24h later, grants both `leave` and `take` for that library. No user record is ever created.

Sessions are **swept**: an expired row is deleted, not merely ignored, on the read paths that can show an item, plus once at `serve` startup. Expiry is still checked on read regardless — a session can be expired but not yet swept, and the read check is what makes that safe in the interval. The two are belt and braces, not alternatives. The sweep is traffic-driven: a box that goes quiet — which is most boxes — keeps its expired sessions, and everything linked to them, until someone next loads the shelf or the process restarts. The startup sweep is the cheap belt for the quiet-box case; it is not a substitute for the traffic-driven read-path sweep, which still has to run, because a long-lived process between restarts gets no other pass.

**A left item never stores a session reference.** A left item is public and permanent, so a link from it would point at durable data from the other side and survive the sweep — everything one person left, kept forever, a user record by another name. Leaves are counted with a bare counter on the session row instead, which counts without linking.

**A take does link**, in its own row, and that row is deleted along with the session that made it. A take is a private act against a shelf, not a public contribution, and a link that — once its session is actually swept — cannot outlive the session's 24 hours is not a user record — it is what makes an undoable take possible at all. That bound holds only once the sweep has run: on a quiet box, or between the startup sweep and the next shelf or item read, an expired session's take rows persist past the nominal 24 hours, exactly as long as the session row itself does. The cost that remains: a revoked card's *left* items still cannot be retracted, because leaves stay unlinked; that follows from presence being attestable rather than enforceable.

---

## 4. Presence: tokens, codes, and the booklet

### The two codes

- **Browse code** — stable, generated once, printed once, mounted permanently. Encodes `https://host/`. Reads *"Scan to browse the shelf — anyone, anywhere, anytime. No code needed."*
- **Leave/take code** — rotating monthly, swapped by the steward from a printed booklet. Encodes `https://host/s/<token>`. Reads *"Scan to leave or take."*

### Vocabulary (amended by Milestone 5)

**"The shelf" names both the digital list and the physical spot.** A reader meets "the shelf is empty" on the page and "scan the card at the shelf" on the sign in the same breath, and that is intended — they are the same thing, and one noun is easier to hold onto outdoors in bad light than two.

**The two printed artifacts have names, not descriptions:** the browse code above is user-facing copy's **shelf code**, and the leave/take code is the **take/leave card**. User-facing copy says one of those, never "the box," "the box door," "the hinge," or "Little Free Library." An LFL is one demographic; a Boulevard may be a sandwich board, a garage door, or a fence, and copy that assumes a box excludes every steward who has none. `TestNoTemplateAssumesALittleFreeLibrary` (`internal/web`) and its `internal/booklet` counterpart walk the embedded templates and the booklet cover for exactly those strings.

This is a change to what a reader is shown, not a find-and-replace across the repository — this document's own prose, code comments, and commit messages go on saying "the box" where it reads naturally.

**Neither artifact's copy claims a scan distance.** The two QR codes are 1.5in and 1.15in as of Milestone 5 (§13) — both readable only up close — so nothing user-facing says "from the sidewalk" or "from across the yard" until Milestone 5.5 makes one of them big enough for that to be true.

### Token scheme

At install, generate **12 tokens** (128 bits of entropy, base32, no ambiguous characters). Each is assigned an explicit calendar period.

Period 1 runs from the install date to the end of that calendar month, however short. Periods 2–12 are whole calendar months. A short first period is correct: it keeps every later card aligned to a month boundary, which is what a steward can actually remember.

```
tokens
  id
  secret          TEXT  -- stored in plaintext, see below
  period_index    INT   -- 1..12 in the first booklet; monotonic across rotations (§6)
  valid_from      DATE
  valid_until     DATE
  printed_from    DATE  -- the period as minted; names the card, never modified
  state           TEXT  -- pending | active | expired | revoked
  first_seen_at   TIMESTAMP NULL
```

**`printed_from` is what names a card**, and it is stored rather than derived because neither endpoint survives the steward overrides below. Force-activate rewrites `valid_from`, so deriving the label from it made an October card show up everywhere as August the moment it went into the door early — including on the confirmation that asks whether to discard secrets while naming the card it will keep. `valid_until` is no refuge: extend moves that one. The label is set once, at mint, and never modified; the dates beside it move freely. This is the field the paragraph below depends on when it says the divergence is acceptable.

**Do not derive codes from a time function (TOTP-style).** Server clock drift, DST, and a steward who swaps the card four days late all become silent auth failures with no recovery path. Explicit stored periods make every failure inspectable and fixable.

**Validation on scan:**

1. Look up token by secret. Unknown → generic failure page.
2. `state == revoked` → generic failure page.
3. Today within `[valid_from - grace, valid_until + grace]`, grace = **7 days**. The grace window is what makes a human-swapped paper card work.
4. Outside the window but token is otherwise valid → a diagnostic page, distinct from failure, and **which one depends on the side**. Past the window plus grace: "this card is out of date, the steward needs to swap it", naming the day it stopped working. Before the window plus grace — a spare card from the booklet, or a card swapped in early — "this card isn't in use yet", naming the day it starts working, with the year when that day falls in another calendar year. Telling the holder of next July's card that it is out of date and stopped working on a date that has not happened sends a steward looking for a fault that does not exist.
5. On first valid scan, set `first_seen_at`. Mark the token `active` **only if its period is later than the current active token's**, expiring the previous active token when it is. A token older than the current active one still grants a session and still records `first_seen_at`, but does not become active — otherwise a stray card found in a drawer could rewind the steward's sense of which card is in the door.

**Steward overrides:** force-activate any pending card (swapped early), extend the current card (booklet lost, replacement not printed yet), revoke a card (sheet stolen or photographed). Force-activate works by moving `valid_from` to today, not by writing `state` alone — validation above reads `state` only for `revoked`, so a card whose period has not started would still answer `NotYet` no matter what `state` says.

The printed card then disagrees with the database: a card reading "Oct 1 – Oct 31" will have a period starting today. That is accepted rather than fixed, because the steward has physically put that card in the door and the month name is how they identify it — which is exactly why the name comes from `printed_from` and not from a field these overrides rewrite. A steward holding the October card must find a row saying October, whatever its dates now read.

**Reprinting.** Secrets are stored in plaintext so the booklet can be regenerated at any time. This is a deliberate trade: the threat model is a steward's own server, and if that server is compromised the shelf is already lost. The alternative — hashed secrets — makes a lost booklet unrecoverable, which is a far more likely event than server compromise. Reprint regenerates the PDF for the newest booklet's twelve cards, whatever state each is in — a booklet is a physical sheet, and a reprint has to reproduce the sheet the steward is holding, including the card already in the door. It is selection by booklet, not by state: after a rotation the surviving active card belongs to the *older* booklet and is deliberately not on the reprint, because it is already taped inside the box.

**Rotation:** the steward mints a fresh twelve at any time — the sheet was photographed, or the booklet is running out. It replaces the pending cards rather than waiting for them to expire, and leaves the card in the door alone. See §6 for what that costs and why the active card survives.

### The booklet

The booklet is a permanent part of the design (§9), not a placeholder for hardware. It is the project's only physical artifact, the thing a steward handles monthly and shows a curious neighbor, and it deserves the same design attention as the shelf page.

A single printable PDF, US Letter, generated at install:

- Twelve tear-off cards, one per month, each with a QR code, the month name, and the period dates.
- Sized to fit whatever a steward tapes inside a box door. Aim for roughly business-card proportions.
- A cover sheet with the browse QR, install instructions, and the signage line.
- Cut lines. Assume scissors, not a guillotine.

**Signage line:** `Take something. Leave something. Scan to leave.`

The mounted sign carries that line. **The monthly card says "Scan to leave or take"** — §5 gates taking behind a session identically to leaving, and a visitor who reads only "scan to leave" will tap *take* and find the control inert. Each code names its own verb, and the two artifacts deliberately do not resemble each other: browse is mounted, light, landscape, undated; leave/take is inside the door, dark-barred, dated, business-card sized.

### Session mechanics

A valid scan sets an `HttpOnly`, `SameSite=Lax`, 24-hour cookie carrying an opaque random session id. A server-side session row records which token minted it, and that row is the source of truth — the id is not signed, because the lookup already rejects anything never issued and a signing key would add something to store, rotate and lose while defending against nothing. It would also break §10's promise that ejecting a library is a file copy.

Sessions are swept lazily on the shelf and item read paths, immediately before either queries: an expired row is deleted outright rather than left to accumulate. Expiry is still checked on read as well — a session can be expired for a while before any traffic sweeps it, and the read check is what keeps that interval safe. The two are belt and braces, not alternatives; sweeping does not replace the check.

**The 24-hour window is intentional.** Nobody wants to compose a thoughtful note standing outside a box in the rain. Scan on your walk, write at your kitchen table. The physical act stays mandatory; the typing does not.

Presence is **attestable, not enforceable.** A scanned URL can be texted to a friend. Rotation plus rate limits bound the blast radius, and that is enough. Do not attempt to make it airtight.

**Per-session rate limits:** 3 leaves, 3 takes.

---

## 5. Shelf mechanics

### Leaving

Requires a session. Item enters the **approval queue** (steward-configurable; default on).

On approval: `copies_total = copies_left = default_copies`, state `shelved`.

If the shelf is full, the **oldest non-pinned item** is evicted to the shed. FIFO, deliberately dumb, requires no steward labor, and — critically — is not attention-weighted. Letting popular items survive longer would rebuild the algorithmic feed this project exists in reaction to.

**A link with no scheme is assumed `https`.** Typing `https://` on a phone keyboard, one-handed, outdoors, is real friction on the one form a passerby ever fills in, and a bare domain is unambiguous about what was meant. The test for "no scheme" is textual rather than `url.Parse`'s `Scheme` field, because Go reads `example.org:8080/x` as scheme `example.org` — and it only ever *supplies* a missing scheme, never replaces one. That second half is load-bearing: prepending whenever the scheme is not http or https would turn `javascript:alert(1)` into `https://javascript:alert(1)`, an href far worse than the rejection it replaced. Anything that named a scheme still meets the http/https check untouched. The accepted cost is that a bare host:port matches the scheme grammar and is not helped.

### Taking

Requires a session. Decrements `copies_left`. At zero, the item moves to the shed.

Taking is a **write**. It changes shelf state, so it is gated exactly like leaving. This is why the earlier framing ("read is global, write is local") needed correcting: take *feels* passive because you are receiving, but on a finite shelf, removal is as much an edit as addition.

Copies exist so that no single tap destroys anything, and so that popular items cycle faster than ignored ones — which is exactly how a good LFL steward behaves with a book that keeps disappearing.

Take is **one tap**: it records the take and opens the thing in the same motion, with no separate confirmation step. It is `POST` only — a `GET` take URL would be shareable, prefetchable, and, because it redirects to the item's own payload, an open redirect wearing the shelf's domain.

Take is **undoable for as long as the session lasts**. Undo restores the copy and, if the take was what shed the item, returns it to the shelf. Undo and re-shelving both **refuse a full shelf rather than evicting** something else to make room — a stray tap, or a steward's misjudged re-shelve, must not cost a *different* item its place. The copy is still returned even when the shelf-side of the refusal fires, so the refused take stops counting against the session's limit either way.

### Expiry

Any item older than `max_age_days` moves to the shed, regardless of shelf pressure.

Without this, a low-traffic box — which is most boxes — has items sitting for two years. The shelf stops being a shelf and becomes a plaque. FIFO alone only evicts under pressure, and the boxes that most need churn are precisely the ones that will not get it.

### The shed

Evicted items are not public and not deleted. The shelf **sheds** them — passively, the way a tree sheds leaves, with no judgment implied. They sit in the steward's admin view, where the steward may **re-shelve** or **release** (soft delete). This mirrors what a real steward does with a book that has been sitting too long, and it keeps the decision human.

Items record **how** they reached the shed — evicted off a full shelf, expired past `max_age_days`, or taken to zero copies. These read very differently: an expired item is stale, an item taken to zero is the opposite. The reason informs the steward's decision and nothing automatic — it is a label for a human to read, not an input to any ranking. Do not build automatic re-shelving of popular items on top of it.

**Do not turn re-shelving into a job.** Every mechanic that generates steward labor pushes toward the steward-blog failure mode, because the person doing the most work starts feeling like the author.

### Pins

Max 3. Never evicted, never expire, no copies, cannot be taken. For the "what is this" explainer and, if the box is a mutual aid station, a link to whatever the steward already uses for donations.

### Steward additions

**Go through the same presence flow.** The steward owns the server and nothing technically stops them adding remotely, but a box in a low-traffic spot will otherwise drift toward 90% steward content. They live there; the walk is free. The software should make the honest path the default one.

---

## 6. Surfaces

### Public (no session)

| Route | |
|---|---|
| `GET /` | The shelf. |
| `GET /i/:id` | Single item. Shareable. |
| `GET /about` | What Boulevard is, where this box is, who tends it. |
| `GET /s/:token` | Scan endpoint. Validates, mints session, redirects to `/`. |

The shelf page is the primary surface and it is used **one-handed, on a phone, outdoors, in bad light, possibly in winter.** Design for that, not for a desktop browser. Large tap targets, high contrast, no hover states, fast on cold cellular.

Every item shows its note prominently and its `copies_left`. Without a session, leave and take controls are visible but inert, with a short explanation: *"Scan the card at the shelf to take or leave."* Visible-but-inert, not hidden — a remote reader should understand the rule, not think the feature is missing.

### Orientation, and the empty state

**Milestone 5 corrects a claim made here.** This section used to call the empty state "arguably the most important screen in the application." That overstated it: empty is rare, mostly a steward's own first hour before anyone has left anything. The gap that actually recurs is orientation, and it is missing on **every** shelf, not only an empty one — a stocked shelf shows notes and links but never says what the thing is or how it changes, so someone arriving from a shared link at a full shelf is exactly as lost as someone arriving at an empty one.

The fix is one line above the items, *"Things people leave for each other,"* shown **unconditionally** — whether or not the reader holds a session. Session state is not a proxy for understanding: someone may scan the take/leave card *precisely because* they are trying to work out what this is, and gating the explanation on a session would hide it from exactly the person asking.

With the orientation line in place, the empty state stops being a special screen. **It is the ordinary shelf page with nothing in the middle** — same line at the top, same layout, no items between it and the leave button. It is still the honest outcome, and it is still an invitation; it no longer needs a design pass of its own to be one.

### With session

Leave form (type, payload, note, optional attribution) and an active take control on each item. A persistent, quiet banner: *"You're at the shelf. Until [time]."*

**The banner names no verbs.** It once read *"You can leave or take until…"*, which asserted a capability the controls below it could have already withdrawn — most starkly on the leave form, where it sat directly above a disabled button explaining that three leaves were already spent. A line that renders on every page in every state cannot also be an accurate summary of what is left; presence and allowance are different facts. The banner states presence and its deadline; the inert controls state the allowance, each in its own place (§5's per-session limits).

### Steward

**The credential is generated, not chosen.** `boulevard steward-key` mints a 128-bit key, prints it once, and stores only its SHA-256 hash. Running the command again replaces the key outright and revokes every live steward session on that library in the same transaction — that is the reset path, and it is only a reset path if a session minted under the old key stops working the moment the key is replaced. A lost key has no recovery: there is no artifact to reprint, so generating another costs nothing and there is nothing to keep recoverable. This is not the same decision as the deliberately plaintext token secrets above, and the difference matters: a token secret must stay recoverable because reprinting a lost booklet must always work, while a steward key protects nothing worth reprinting. `boulevard init` will not implement key generation a second time — it calls `steward-key`, so the two cannot drift apart.

Before a key exists, **every steward route 404s**, including the login form — not 403, and not a setup page. A fresh install should not advertise a surface that is not armed, the same instinct behind unknown and revoked token secrets producing identical responses. This includes a **method mismatch**: the steward mutations are POST-only, and method matching happens in the mux, before any handler runs, so a GET at one of them never reaches the code that enforces this rule. Left alone, `net/http` answers it with 405 and an `Allow` header — which advertises more than the 403 forbidden above, since it also confirms the verb, and does so for a slug that need not even exist. A 405 is therefore rewritten to a byte-identical 404 for the whole server. Sessions are long-lived (thirty days) but live in their own table, not the presence sessions table, because the presence sweep depends on having no exceptions to sweep around.

- Approval queue
- Shelf: remove, pin, unpin
- Shed: re-shelve, release. Removing an item from the shelf sheds it rather than releasing it outright, recording `removed` as its own reason alongside `evicted`, `expired` and `taken` — a misfire is recoverable by re-shelving, and release stays the deliberate second step.
- Tokens: twelve rows, one per period — month, dates, state, whether it has been seen, the active card marked. Inline force-activate, extend, revoke, shown only where legal so the page never offers a tap that can only fail. Below the list: download the booklet PDF, and rotate onto a fresh twelve. **Revoke and rotate confirm first**, on their own page, because they are the two acts that cannot be undone and the desk is the one-thumb surface — a phone must not offer an irreversible act with less friction than the terminal does. Each confirmation names what it costs: how many secrets a rotation discards, and — when the card being revoked is the one in the door, or when nothing has been scanned so a rotation discards every printed card — that the box stops working until someone walks to it with a card that still scans.
- Settings: name, location, slots, max age, default copies, approval on/off
- Export: the entire library as one runnable SQLite file, with a plaintext warning at the point of download

Four decisions from the token-management design (`docs/superpowers/specs/2026-08-22-token-management-design.md`) are worth recording here because they shape the surfaces above. **Rotate leaves the active card alone.** It discards every `pending` token and mints twelve fresh ones, but the card currently in the door is untouched — the alternative would mean an action taken at a keyboard makes the box stop working until someone walks to it, and killing the live card on purpose stays available as its own act: revoke. **`period_index` is monotonic across booklets, not 1–12.** `UNIQUE (library_id, period_index)` means a rotation cannot reuse 1–12 while the active card still holds one of them, so the second booklet mints 13–24, the third 25–36; `booklet` and `card` are derived from `period_index` rather than stored, and a card's *printed* number is its position in the booklet being laid out, not its index. **A rotation's new booklet never starts before today.** It begins the day after the active card ends, so no gap opens between the card in the door and the next one — but if that card lapsed months ago, following it blindly would mint cards already past their window plus grace on the day they are printed. §4 keeps periods explicit precisely so a steward who swaps the card late is fixable rather than silently broken; handing that steward dead cards is the same failure moved somewhere new. **A pending card that has been seen is revoked, not deleted, when a rotation discards it.** Activation moves forward only (§4), so a lower-numbered card scanned inside its own window grants a session and stays `pending` — and `sessions.token_id` references it. Deleting that row would fail the foreign key and make rotation refuse, at exactly the moment (a photographed sheet) a steward most needs it; `revoked` discards the secret just as completely while leaving the session to expire on its own.

---

## 7. Views and takes are different numbers

`views` is global and potentially large. `takes` is local and small. For a steward these are genuinely different signals — "the internet found this" versus "three neighbors wanted it."

Show both. Takes drive the copies mechanic. **Views drive nothing at all.** Views influencing shelf state is how an algorithm sneaks back in.

Item pages display takes as e.g. `Taken 3 times`.

---

## 8. Install

Target: **one command to a running server with a printable booklet in under sixty seconds.**

```
boulevard init --name "The Fairview Boulevard" --location "4th & Fairview" --base-url https://boulevard.example.org
```

Creates the SQLite database, generates the token booklet, prints the steward key once, writes `boulevard-booklet.pdf` to the working directory, and starts the server. It prints the key by calling `boulevard steward-key` rather than generating one itself, so the two commands cannot drift apart — there is exactly one place key generation happens.

**`--base-url` is required, and verified before any PDF is written.** Every token and the permanent browse sign encode this host. A steward who prints before their domain, tunnel, or port is settled ends up with twelve dead cards and — far worse — a wrong browse sign, which is the one artifact meant to be permanent (§10). If the URL does not resolve, refuse to generate and say why. A steward who cannot yet answer "what is my URL" is not ready to print.

`boulevard booklet` regenerates at any time, including after a base URL change. Regeneration is always cheap; a mounted sign is not.

Milestone 0 ships this command standalone, before any server exists:

```
boulevard booklet --name "..." --location "..." --base-url https://... --out booklet.pdf
```

Milestone 4b adds token management and export as CLI siblings of the web admin, the same way the approval queue and the shed stayed CLIs after 4a's desk shipped:

```
boulevard tokens         [--db boulevard.db] [--slug SLUG]
boulevard force-activate [--db boulevard.db] [--slug SLUG] <handle>
boulevard extend         [--db boulevard.db] [--slug SLUG] <handle>
boulevard revoke         [--db boulevard.db] [--slug SLUG] <handle>
boulevard export         [--db boulevard.db] [--slug SLUG] --out FILE [--force]
boulevard booklet ... --rotate
```

`tokens`, `force-activate`, `extend` and `revoke` address cards by handle, not by id — a steward is holding a card, not typing a 26-character identifier — but the handle is `period_index`, not the number printed on the card. `boulevard tokens` prints both together, `[13]  Booklet 2 · Card 1`, precisely because the second booklet's first card carries a printed "1" while its handle is 13 (§6). The bracketed number is what the mutation commands accept; the friendlier label beside it is only how the steward identifies the physical card in hand. `--rotate` is `booklet`'s write path; the plain command already reprints an existing library's twelve cards unchanged, so `--rotate` is what mints a new twelve instead.

Also ship a single-service `docker-compose.yml`.

No config file required for a working install. If a config file is required, the install story has failed. Agent-assisted install is a nice-to-have, not a design goal — if you hit sixty seconds an agent is unnecessary, and if you miss it an agent will not save you.

Storage: one SQLite file plus an image directory. Backup is copying a folder.

Images: resize on upload, strip EXIF (**including GPS**), cap dimensions, cap file size. A stranger uploading a photo should not silently publish their home coordinates.

---

## 9. Non-goals for v1

- **Donations.** Payments mean chargebacks, KYC, disputes, and a self-hosted steward suddenly holding other people's money. It also inverts the emphasis: a donate button quietly makes the project about money. Mutual aid stations pin a link to their existing Venmo or Ko-fi. Zero code, zero liability.
- **Federation.** ActivityPub is a tarpit that will eat two years. A later neighbor directory is a plain list of URLs.
- **Accounts, profiles, follows, notifications, comments, likes.**
- **Hosted multi-tenancy.** v1 ships one install, one boulevard — but the data model is built for many from day one. See §10.
- **Hosted video.**
- **Full-text search.** Twelve items.
- **An e-ink display for the rotating code.** Rejected on the merits, not deferred. Swapping the paper card is the one thing that reliably walks a steward to their box every month, and while they are there they notice the sagging hinge, the faded browse sign, the water damage, the actual paper note someone left. Automating rotation removes the only guaranteed maintenance visit. Secondary costs: a powered device needs power and weatherproofing through a Minnesota winter, and it makes an otherwise worthless box worth stealing from. Paper is the design, not the fallback.
- **Traffic-scaled shelf size.** `slots` is set by the steward and never adjusts itself. Auto-scaling capacity with traffic is engagement ranking wearing a different hat: busy boxes grow, quiet ones stay small, and the software starts having opinions about which places matter. The steward changing the number by hand is the only version of this that should exist.
- **Native apps.**

## 10. Multi-tenancy — architected now, shipped later

**Target:** a digitally savvy neighbor hosts Boulevard for the boxes around them. Bryn Mawr has 6–12 Little Free Libraries within a mile. That person is a real user, and the WordPress ecosystem is the model — except that here, *many* people run the multi-tenant version, not one company.

This splits into one cheap obligation for v1 and a set of expensive features for v2.

### v1 obligation: never assume a singleton

- `library_id` is a first-class key on every item, token, session, and setting.
- Every query is library-scoped. No global "the library" object, no implicit current-library from configuration.
- Routes carry the library: `/b/:slug/...`. In single-library mode the server may redirect `/` to the sole slug, but the canonical route always includes it.
- Token secrets are unique across the host, not per library.

That is the whole v1 cost. Everything below is deferrable *because* of it.

### Storage: one database file per library, plus a registry

The registry database holds libraries, slugs, stewards, and map coordinates. Everything else — items, tokens, sessions — lives in that library's own SQLite file, alongside its own image directory.

**This makes ejection a file copy.** A steward who outgrows the host, distrusts the host, or simply wants their own box takes `fairview.db` and its images and runs the standalone binary. No export format to maintain, no lossy migration, no permission required. A host who loses interest hands out twelve files and dissolves cleanly.

For a project about freedom in both senses, the ability to leave without asking is worth architecting around.

The trade: no joins across libraries. Since the only legitimate cross-library views are a map and a private moderation queue, this is a constraint worth having.

### Printed signs outlive hosting arrangements

A browse QR encodes a URL. If the host's domain dies, every box it served has a dead sign screwed to it — the digital part ejects cleanly, the physical artifact does not.

Design responses:

- **The browse sign is replaceable by design.** A printed card in a sleeve, never a vinyl decal or engraved plate. State this in the install docs and in the booklet cover sheet.
- **Hosts take on an implied redirect obligation.** Project norm: if you shut down, keep redirects alive for a year. `boulevard export-host` emits a static redirect map as its last artifact to make that trivial.
- **Stewards who want permanence point their own domain at the host.** Then ejection is a DNS change and nothing physical moves.

The rotating leave/take cards reprint monthly regardless. The permanent browse sign is the exposure.

### Steward accounts — the narrowest possible version

Multi-tenancy forces the one thing v1 forbids. The rule that actually matters survives intact: **passersby never have accounts, and presence remains the only credential for mutating a shelf.**

Keep steward identity minimal:

- No public signup. The host invites by link; the steward sets a password.
- A steward belongs to one or more libraries. That is the entire model.
- No profiles, no display names, no cross-library identity, no social graph.

If it ever grows a follower count, something has gone wrong.

### Host surfaces

- **Neighborhood map.** Pins for each library, each linking to that shelf. Public.
- **Moderation queue.** Recent items across all hosted libraries. **Private, admin-only.** A host who cannot see what is on their own server cannot answer a takedown.
- **Per-library quotas.** Storage cap, image count, rate limits.
- **Suspend / eject** a library.

### Explicitly forbidden: the host-wide public feed

A "recent across all shelves" page is four lines of code and would be the most-visited page on any host. **Do not build it.** It is the algorithmic feed entering through the service entrance, and it detaches items from the places that give them meaning.

A map sends you to a place. A feed brings the places to you. Only the first is compatible with the thesis.

---

## 11. Pop-up boulevards — deferred, but not retrofittable

A Boulevard does not need a permanent box. A farmers market stall, a block party, a conference hallway, a gallery opening, a family reunion, a memorial — any bounded place that exists for hours or days can carry a shelf.

The thesis holds unchanged: consumption global, mutation local. What changes is that the *place itself* is temporary, and that has a consequence the standing case does not have.

### The sealed artifact

When the last period ends, the shelf goes **read-only, permanently.** Presence was required to write; the place no longer exists; nobody can ever add to it again. Read stays global and permanent.

What remains is a small bounded record of what specific people left while standing in one specific place on one specific afternoon — frozen at the moment the place stopped existing. This falls out of the existing architecture for free, and it is the reason the pop-up case is worth building rather than a novelty.

A sealed shelf says so plainly, in the same register as the empty state (§6):

> This Boulevard was open at the Kingfield market, June 14. It's closed now.

That line is what makes an expired pop-up read as *finished* rather than broken.

### What differs from a standing boulevard

| | Standing | Pop-up |
|---|---|---|
| Period length | Calendar months | Hours or days |
| Codes | Two (permanent browse + rotating) | One. Permanence is not the point. |
| Expiry (`max_age_days`) | On | Off. Nothing outlives the event anyway. |
| Eviction at capacity | FIFO to the shed | **None.** The shelf fills and says so. |
| Take | Decrements copies; item leaves at zero | Counter only. Nothing is removed. |
| The shed | Active | Unused |
| Approval queue | Default on | Default off |
| Printed artifact | 12-card booklet | One card |
| End state | Ongoing | Sealed, read-only, forever |

**Why eviction and copy-decrement are off.** In the standing case, both exist to keep a living shelf fresh. In the pop-up case the shelf *is* the record, and either mechanic would quietly delete parts of it before sealing. A pop-up shelf that reaches capacity is full, and "full" is a legitimate end state for a bounded event.

**Why approval defaults off.** Nobody moderates a queue during their own wedding. Physical presence at a private or ticketed event is already a strong filter. The host may still turn approval on, and should for anything public-facing like a market stall.

### v1 obligation

One thing only: **do not bake "month" into the period type.** Periods are already stored as explicit `valid_from` / `valid_until` values rather than derived from a clock function (§4), so the scheme supports arbitrary lengths. Keep it that way, and never assume a period boundary falls on the first of a month.

**What v1 does not yet satisfy, recorded so it is not mistaken for done:** those columns hold calendar dates, and `boulevard.Date` is deliberately a bare date with no time and no timezone (§4). That is correct for month-long periods and cannot express a stall that opens at nine and closes at two. Hour-scale pop-ups therefore need a `Date` → timestamp widening — a migration and a change to validation's day-boundary comparisons, but not a redesign, because the boundaries are already explicit stored data rather than a function of the clock. That is the whole point of the obligation above.

Everything else is additive and can wait.

### Later additions

- `mode` on the library: `standing` (default) or `popup`
- `sealed_at` timestamp; all mutation routes refuse once set
- `boulevard booklet --popup --opens ... --closes ...` emitting a single card rather than twelve
- A `--format` flag on card generation: `yard`, `counter`, `card`, `tent`. The sign layout is the same at every size; only the trim differs.

### Open

Whether a sealed pop-up should be exportable as a keepsake — a single PDF of the shelf, for the host to send round afterward. Appealing, and a natural fit for a reunion or a memorial. Out of scope until the base case works.

---

## 12. Later

Slideshows · richer text · neighbor directory · import from export file

---

## 13. Build order

**Milestone 0 — the booklet generator.** Before any media handling, before the shelf, before storage: the standalone `boulevard booklet` command (§8). Twelve time-gated tokens, base URL verification, and a printable PDF. No server, no items, no database beyond what the tokens need.

Done means: printed on paper, cut with scissors, and a phone scans every card successfully. Budget more of this milestone on the PDF's design than on the token logic — the tokens are a solved problem, the artifact is not, and this is the project's only physical surface.

This is the smallest artifact that proves the whole model. If it feels awkward, that is worth knowing before anything depends on it.

**Milestone 1 — presence.** Scan endpoint, validation with grace, session cookie, the banner.

**Milestone 2 — the shelf.** Item model, leave form, browse page, approval queue.

**Milestone 3 — mechanics.** Copies, take, FIFO eviction, expiry sweep, the shed.

**Milestone 4 — steward.** Full admin, token management, export.

**Milestone 5 — polish.** The empty state, cold-cellular performance, one-handed outdoor usability, the about page.

It also carried a **deliberate look-and-feel sweep**, mostly non-functional, called for after the Milestone 4b acceptance run: the bones were right, but **copy, ordering and type sizes** each needed a pass. Named there or found since — a rotate confirmation whose most consequential sentence was the smallest type on the page; a destructive control sitting closer to a benign one than anything else on the tokens page; and copy that assumed a Little Free Library. Delivered: a `.consequence` type class and an explicit size scale; a control-placement rule keeping destructive actions last and 44px clear of anything benign; the vocabulary sweep of §4 across templates and the booklet cover; and an unconditional orientation line, which is also what corrected §6's empty-state claim (above). This was a sweep, not a rewrite — nothing in it changed what the software does. Left for later because both are behavior rather than look-and-feel: `boulevard booklet`'s reprint still asking for a base URL that is printed on a mounted sign, and the tokens page accumulating revoked rows after rotation.

**Milestone 5.5 — the shelf code.** The two printed codes are 1.5in and 1.15in (§4), both readable only up close — Milestone 5's copy had to distinguish the two artifacts by what they do, never by how far away they work, because the size difference the copy would like to lean on is not yet real. 5.5 gives the shelf code its own full page, 6–7in, large enough to read from several feet rather than inches, and re-records the golden booklet PDF against the new geometry. Only after that may copy claim a scan distance — `TestNoTemplateAssumesALittleFreeLibrary` and its booklet counterpart (§4) ban "from the sidewalk" and "from a distance" until this milestone lands, precisely so the claim cannot arrive before the artifact does.

**Milestone 6 (v2) — the host layer.** Registry database, steward invites and accounts, neighborhood map, private moderation queue, quotas, `export-host` with redirect map. Reachable without refactoring only if §10's v1 obligation was honored throughout.

---

## 14. Open

- Name: "Boulevard" is a working name, common as a word, and shares it with a well-known brewery. No category conflict for an open-source project, but SEO will be a fight and the wordmark must do some work. Not covered by the license (§2).
- Contact Little Free Library, St. Paul. Lead with proximity, ask for fifteen minutes, not for a name.
