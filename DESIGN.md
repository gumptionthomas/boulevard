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
- **The name is not covered by the license.** Trademark and identity are separate; see §13.

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
| `state` | `shelved` \| `shed` \| `released` |
| `pinned` | Steward only, max 3 |
| `left_at` / `shelved_at` | |
| `views` / `takes` | Separate counters. See §7. |

**Video is never hosted.** A YouTube/Vimeo/PeerTube URL is a `link` that renders with an embed.

The `note` is mandatory and is the point. The media is the excuse.

### Presence session

Ephemeral. Created by scanning the rotating code, expires 24h later, grants both `leave` and `take` for that library. No user record is ever created.

---

## 4. Presence: tokens, codes, and the booklet

### The two codes

- **Browse code** — stable, generated once, printed once, mounted permanently. Encodes `https://host/`. Reads *"Scan to browse the shelf — anyone, anywhere, anytime. No code needed."*
- **Leave/take code** — rotating monthly, swapped by the steward from a printed booklet. Encodes `https://host/s/<token>`. Reads *"Scan to leave or take."*

### Token scheme

At install, generate **12 tokens** (128 bits of entropy, base32, no ambiguous characters). Each is assigned an explicit calendar period.

Period 1 runs from the install date to the end of that calendar month, however short. Periods 2–12 are whole calendar months. A short first period is correct: it keeps every later card aligned to a month boundary, which is what a steward can actually remember.

```
tokens
  id
  secret          TEXT  -- stored in plaintext, see below
  period_index    INT   -- 1..12
  valid_from      DATE
  valid_until     DATE
  state           TEXT  -- pending | active | expired | revoked
  first_seen_at   TIMESTAMP NULL
```

**Do not derive codes from a time function (TOTP-style).** Server clock drift, DST, and a steward who swaps the card four days late all become silent auth failures with no recovery path. Explicit stored periods make every failure inspectable and fixable.

**Validation on scan:**

1. Look up token by secret. Unknown → generic failure page.
2. `state == revoked` → generic failure page.
3. Today within `[valid_from - grace, valid_until + grace]`, grace = **7 days**. The grace window is what makes a human-swapped paper card work.
4. Outside the window but token is otherwise valid → show a "this card is out of date, the steward needs to swap it" page. Distinct from failure; it is diagnostic information the steward needs.
5. On first valid scan, set `first_seen_at` and mark `active`. Expire any earlier `active` token.

**Steward overrides:** force-activate any pending card (swapped early), extend the current card (booklet lost, replacement not printed yet), revoke a card (sheet stolen or photographed).

**Reprinting.** Secrets are stored in plaintext so the booklet can be regenerated at any time. This is a deliberate trade: the threat model is a steward's own server, and if that server is compromised the shelf is already lost. The alternative — hashed secrets — makes a lost booklet unrecoverable, which is a far more likely event than server compromise. Reprint regenerates the PDF for all `pending` periods.

**Rotation past month 12:** the steward generates a new booklet. Old tokens expire naturally.

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

A valid scan sets an `HttpOnly`, `SameSite=Lax`, 24-hour cookie carrying a signed random session id. Server-side session row records which token minted it.

**The 24-hour window is intentional.** Nobody wants to compose a thoughtful note standing outside a box in the rain. Scan on your walk, write at your kitchen table. The physical act stays mandatory; the typing does not.

Presence is **attestable, not enforceable.** A scanned URL can be texted to a friend. Rotation plus rate limits bound the blast radius, and that is enough. Do not attempt to make it airtight.

**Per-session rate limits:** 3 leaves, 3 takes.

---

## 5. Shelf mechanics

### Leaving

Requires a session. Item enters the **approval queue** (steward-configurable; default on).

On approval: `copies_total = copies_left = default_copies`, state `shelved`.

If the shelf is full, the **oldest non-pinned item** is evicted to the shed. FIFO, deliberately dumb, requires no steward labor, and — critically — is not attention-weighted. Letting popular items survive longer would rebuild the algorithmic feed this project exists in reaction to.

### Taking

Requires a session. Decrements `copies_left`. At zero, the item moves to the shed.

Taking is a **write**. It changes shelf state, so it is gated exactly like leaving. This is why the earlier framing ("read is global, write is local") needed correcting: take *feels* passive because you are receiving, but on a finite shelf, removal is as much an edit as addition.

Copies exist so that no single tap destroys anything, and so that popular items cycle faster than ignored ones — which is exactly how a good LFL steward behaves with a book that keeps disappearing.

### Expiry

Any item older than `max_age_days` moves to the shed, regardless of shelf pressure.

Without this, a low-traffic box — which is most boxes — has items sitting for two years. The shelf stops being a shelf and becomes a plaque. FIFO alone only evicts under pressure, and the boxes that most need churn are precisely the ones that will not get it.

### The shed

Evicted items are not public and not deleted. The shelf **sheds** them — passively, the way a tree sheds leaves, with no judgment implied. They sit in the steward's admin view, where the steward may **re-shelve** or **release** (soft delete). This mirrors what a real steward does with a book that has been sitting too long, and it keeps the decision human.

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

Every item shows its note prominently and its `copies_left`. Without a session, leave and take controls are visible but inert, with a short explanation: *"Scan the code at the box to take or leave something."* Visible-but-inert, not hidden — a remote reader should understand the rule, not think the feature is missing.

### The empty state

**An empty shelf is the honest outcome and ships as-is.** It is an invitation: the clearest possible signal that the thing wants something from you. A shelf holding one stale link is sad and reads as abandoned.

This page gets real design attention. It is arguably the most important screen in the application.

### With session

Leave form (type, payload, note, optional attribution) and an active take control on each item. A persistent, quiet banner: *"You're at the box. You can leave or take until [time] tomorrow."*

### Steward

Single password, set at install. Sessions long-lived.

- Approval queue
- Shelf: remove, pin, unpin
- Shed: re-shelve, release
- Tokens: current card, force-activate, extend, revoke, regenerate booklet, download PDF
- Settings: name, location, slots, max age, default copies, approval on/off
- Export: entire library as one file

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

Creates the SQLite database, generates the token booklet, prints the steward password once, writes `boulevard-booklet.pdf` to the working directory, and starts the server.

**`--base-url` is required, and verified before any PDF is written.** Every token and the permanent browse sign encode this host. A steward who prints before their domain, tunnel, or port is settled ends up with twelve dead cards and — far worse — a wrong browse sign, which is the one artifact meant to be permanent (§10). If the URL does not resolve, refuse to generate and say why. A steward who cannot yet answer "what is my URL" is not ready to print.

`boulevard booklet` regenerates at any time, including after a base URL change. Regeneration is always cheap; a mounted sign is not.

Milestone 0 ships this command standalone, before any server exists:

```
boulevard booklet --name "..." --location "..." --base-url https://... --out booklet.pdf
```

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

## 11. Later

Slideshows · richer text · neighbor directory · import from export file

---

## 12. Build order

**Milestone 0 — the booklet generator.** Before any media handling, before the shelf, before storage: the standalone `boulevard booklet` command (§8). Twelve time-gated tokens, base URL verification, and a printable PDF. No server, no items, no database beyond what the tokens need.

Done means: printed on paper, cut with scissors, and a phone scans every card successfully. Budget more of this milestone on the PDF's design than on the token logic — the tokens are a solved problem, the artifact is not, and this is the project's only physical surface.

This is the smallest artifact that proves the whole model. If it feels awkward, that is worth knowing before anything depends on it.

**Milestone 1 — presence.** Scan endpoint, validation with grace, session cookie, the banner.

**Milestone 2 — the shelf.** Item model, leave form, browse page, approval queue.

**Milestone 3 — mechanics.** Copies, take, FIFO eviction, expiry sweep, the shed.

**Milestone 4 — steward.** Full admin, token management, export.

**Milestone 5 — polish.** The empty state, cold-cellular performance, one-handed outdoor usability, the about page.

**Milestone 6 (v2) — the host layer.** Registry database, steward invites and accounts, neighborhood map, private moderation queue, quotas, `export-host` with redirect map. Reachable without refactoring only if §10's v1 obligation was honored throughout.

---

## 13. Open

- Name: "Boulevard" is a working name, common as a word, and shares it with a well-known brewery. No category conflict for an open-source project, but SEO will be a fight and the wordmark must do some work. Not covered by the license (§2).
- Contact Little Free Library, St. Paul. Lead with proximity, ask for fifteen minutes, not for a name.
