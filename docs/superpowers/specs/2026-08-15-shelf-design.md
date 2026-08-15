# Milestone 2 — The Shelf

**Date:** 2026-08-15
**Status:** design approved, unbuilt
**Scope:** the item model, the leave form, the browse page, and the approval queue — `DESIGN.md` §3, §5, §6 and §12.

Milestone 1 made scanning a card grant a session. This milestone gives that session something to do.

---

## 1. What this milestone is

The shelf becomes a shelf. Someone standing at the box scans the card, leaves a link or a note, and it waits for the steward. The steward approves it from the command line, and it appears for everyone on the internet to read.

**Done means:** scan a printed card, leave something, approve it from a terminal, and see it on the shelf from a phone that has never scanned anything.

## 2. Scope

The complete loop for two of the three item types, and nothing beyond it. Milestone 1 built a shelf page with no items; this fills it.

## 3. Non-goals

- **Taking.** Copies, decrementing, and the move to the shed at zero are Milestone 3. The take control renders inert for everyone.
- **Image uploads.** See §4.2.
- **The shed view, re-shelving, releasing, expiry sweeps.** Milestone 3.
- **Steward web admin, pins, settings screens, token management.** Milestone 4. This milestone's approval is a CLI.
- **Rate limiting.** §4's 3 leaves per session is Milestone 3; see §6.3 for what this milestone does instead.
- **Link embeds, fetched titles, thumbnails.** See §7.2.

---

## 4. Decisions

| Decision | Choice | Why |
|---|---|---|
| Approval | A CLI: `queue` / `approve` / `reject` | Keeps §5's default-on without pulling steward auth forward from M4. See §4.1. |
| Item types | `link` and `text`; `image` deferred | Image is a subsystem with a safety-critical part. See §4.2. |
| Full shelf | Evict the oldest to `shed` on approve | The shelf must be finite from the first item. See §8. |
| Link display | Domain only | No fetch, ever, and a hostile URL cannot borrow a title. See §7.2. |
| Form order | Type, payload, note, attribution | §6's order and the shape people expect. |
| Copy count | Shown on the shelf | §6 requires it, even though nothing decrements it yet. |
| Session/item link | None stored | Storing one would create a user record. See §5.2. |

### 4.1 Why approval is a CLI

§12 assigns the approval queue to this milestone, but §6 puts it behind a steward password set at install, and §12 assigns that to Milestone 4. Taken literally, this milestone would ship a shelf that nothing can ever reach — approval is default-on, so every item would sit pending and the browse page would render the empty state forever.

The steward owns the server. Operating on the database from a terminal needs no authentication at all, so a CLI resolves the ordering problem without building half of Milestone 4's admin and without changing §5's default. Milestone 4 replaces these commands with the web queue.

### 4.2 Why image uploads are their own milestone

`image` is not a third enum value. §8 requires resize on upload, EXIF stripping **including GPS**, dimension caps and file-size caps, plus a stored image directory that §10's ejection story depends on ("takes `fairview.db` *and its images*"). §2 notes Go's imaging support is weaker than Python's and names two dependencies this project does not yet have.

The GPS requirement is the deciding factor: *"A stranger uploading a photo should not silently publish their home coordinates."* That is a harm to someone who never opted into anything, and it deserves a milestone where it is the main event rather than one field on a form.

The schema keeps `image` in the type vocabulary, so that milestone migrates nothing.

---

## 5. Two gaps in DESIGN.md

### 5.1 There is no state for an item awaiting approval

§3 lists `shelved | shed | released`. §5 says a left item "enters the approval queue". Those cannot both be true: the queue is a state the model does not name.

This milestone adds **`pending`**, and `DESIGN.md` §3 is amended to list it. An item is `pending` from the moment it is left until the steward approves it (`shelved`) or rejects it (`released`).

### 5.2 Recording which session left an item would create a user record

The obvious schema puts a `session_id` on `items`, which would let Milestone 3 count leaves per session and let Milestone 4 drop a revoked card's items.

It would also violate §1. Sessions are never swept — Milestone 1 checks expiry on read rather than deleting rows — so a session reference on each item is a durable, linkable trail: *these three things were left by the same person, within one 24-hour window, at one box.* That is an identity by another name, and §3 is explicit that a presence session creates no user record.

**No session reference is stored on `items`.** Milestone 3 enforces its per-session limit with a **counter on the session row** instead, which counts without linking. The cost is that Milestone 4 cannot retract a revoked card's items; that is the correct trade, and it follows from the same principle that makes presence attestable rather than enforceable (§4).

---

## 6. The leave flow

### 6.1 Routes

```
GET  /b/{slug}/leave     the form
POST /b/{slug}/leave     submit
```

**Without a session, `GET /leave` renders the form visible but inert**, carrying §6's explanation — *"Scan the code at the box to take or leave something."* — rather than 404ing. Milestone 1 established this: show the rule, do not hide the feature.

`POST /leave` without a live session for **this** library is rejected with **403**, rendering the same inert form and explanation. A session minted at another box grants nothing here, exactly as `liveSession` already enforces on the shelf. A validation failure is **422** with the form re-rendered; a successful submission is **303** to the confirmation page, so a reload cannot leave the same thing twice.

An unknown slug is a 404 on all of these, never a fallback to the sole library — the Milestone 1 rule is unchanged.

### 6.2 Validation

| Field | Rule |
|---|---|
| `note` | Required, non-blank after trimming, at most **1,000 runes**. The one field that cannot be skipped. |
| `type` | `link` or `text`. Anything else is rejected. |
| `payload` (link) | Must parse as `http`/`https` with a host. |
| `payload` (text) | Non-blank, at most **4,000 runes**. |
| `attribution` | Optional, at most 120 runes. |

The 4,000-rune cap is not in `DESIGN.md`. It is a shelf, not a blog, and §11 puts "richer text" under Later; the number is a judgment call recorded here so it is not mistaken for a derived requirement. The 1,000-rune cap on `note` is the same kind of judgment call for the same reason: §3 makes the note required and names no bound, and an unbounded required field is a trivial way to fill a steward's disk.

**Runes, not characters, and not bytes.** Every limit above counts runes — `utf8.RuneCountInString` — so a note in Greek, Cyrillic or Japanese gets the same allowance as one in English. `len()` would give a two-byte script half the room and a four-byte one a quarter. The tests spell this out with `strings.Repeat("é", …)` at each boundary, so a later milestone cannot "simplify" it toward bytes without going red.

Sanitizing is **not** validation's job. A note is stored exactly as it was typed, control characters included; the display layer defuses them at each print boundary (`boulevard.Sanitize`). Rewriting a submission on the way in would make the stored value a lossy copy of what someone wrote, for the benefit of one of its readers.

Validation is a pure function over submitted values, returning field-keyed errors so the form can re-render with what the person typed still in it. Losing a composed note to a validation error is the worst failure this form has.

### 6.3 What happens after submitting

The item is stored `pending` and the browser gets a confirmation page saying plainly that a steward looks at new things first, and that there is nothing further to do.

It must not imply the item is live. Approval is default-on, and a confirmation that reads like publication would make the shelf look broken to the person who just used it.

On a shelf whose steward has turned `approval_required` off (§9), the item is shelved in the same request and the confirmation says so instead. The rule underneath is that the page tells the truth about where the thing went; "a steward looks at it first" is as wrong on that shelf as "it's live" is on a moderated one.

The confirmation deliberately does **not** say how many more things you may leave today. §4's limit of 3 is Milestone 3's to enforce, and printing a count the software is not keeping is a promise it cannot honour.

---

## 7. The browse page

### 7.1 The note leads

§3: *"The note is mandatory and is the point. The media is the excuse."* Every item renders its note first, in the largest type on the card. The source is one quiet line beneath it.

Each item shows its `copies_left` (§6) and an inert take control, greyed for everyone — taking is Milestone 3. Attribution, when present, reads as *"left by the guy with the beagle"*.

Only `shelved` items appear. A `pending` item is not public, and neither is a `shed` one.

**Order: most recently shelved first.** `DESIGN.md` does not say, and the alternative is defensible — oldest first would mirror the eviction order, so a reader would see what is about to leave. Newest first wins because the shelf is read repeatedly by the same neighbours, and what changed since their last look is the thing worth putting at the top. It is also the only ordering that is not attention-weighted while still being useful, which §5 cares about. Ties break by `id` so the order is total and stable.

### 7.2 Links show a domain, and nothing is ever fetched

A link renders as its domain — `youtube.com` — not a title, not a thumbnail, not an embed.

Fetching a title means the server makes an outbound request for every URL an unauthenticated stranger submits. That is an SSRF surface against whatever the box can reach, a way to make the box a request amplifier, and a slow leave form on cold cellular. It also lets a hostile URL dress itself in a borrowed title.

§3 says a video URL "renders with an embed". That is deliberately deferred: embeds are a per-provider allowlist and an iframe policy, and the note already carries the meaning. Recorded in §12 as unbuilt rather than silently dropped.

### 7.3 The single item page

```
GET /b/{slug}/i/{id}
```

§6 calls it shareable, and it is the page a link out of the shelf points at. It increments `views`.

**Neither counter is displayed yet.** §7's point is the contrast between "the internet found this" and "three neighbours wanted it", and until takes can move, every item would read "Taken 0 times" for a whole milestone. The column is incremented; the display waits for Milestone 3.

---

## 8. Approval, and a finite shelf

```
boulevard queue    [--db PATH]        list pending items, oldest first
boulevard approve  <id-prefix>        shelve it
boulevard reject   <id-prefix>        release it
```

An item's `id` is 128 bits of `crypto/rand` in Crockford base32, the same as every other identifier in this project — 26 characters, generated through the existing `boulevard.RandomBase32`.

Nobody types 26 characters at a prompt, so the commands accept **any unique prefix** and `boulevard queue` prints a short handle for each item. The prefix is resolved against that library's pending items only. An ambiguous prefix is an error naming every candidate rather than a guess, and a prefix matching nothing is an error saying so — approving the wrong thing because a prefix silently resolved to it is the failure worth designing against.

The handle is four characters, or the shortest length that is unique across everything the command listed, whichever is longer. Printing four unconditionally makes the ambiguity error unrecoverable: the steward is shown two 26-character ids and has no longer prefix to type for the one they meant.

Matching folds case, and folds the letters Crockford's alphabet leaves out — `O` to `0`, `I` and `L` to `1`. Those letters are excluded (`DESIGN.md` §4) precisely so a human cannot mistype one identifier into another; refusing to fold them throws away the reason for the alphabet and answers "nothing waiting starts with that" for an item on the screen.

**Approving** sets `copies_total = copies_left = default_copies`, `state = shelved`, `shelved_at = now`.

**If the shelf already holds `slots` items, the oldest is evicted to `shed` in the same transaction.** §5's rule is "the oldest non-pinned item", and nothing is pinned in this milestone, so it reduces to the oldest by `shelved_at`. FIFO is deliberately dumb and deliberately not attention-weighted — letting popular items survive longer would rebuild the ranking this project exists in reaction to.

Eviction lands here rather than in Milestone 3 because a shelf that grows past its `slots` is not the object this design describes. A finite shelf is why taking counts as a write at all. The shed *view* is still Milestone 3's; this milestone records the state without offering a way to browse it.

**Rejecting** sets `state = released`. §5 calls release a soft delete: the row stays, so a steward who rejects the wrong thing has not destroyed it.

---

## 9. Schema

```sql
CREATE TABLE IF NOT EXISTS items (
    id           TEXT PRIMARY KEY,
    library_id   TEXT NOT NULL REFERENCES libraries(id),
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL,
    note         TEXT NOT NULL,
    attribution  TEXT NOT NULL DEFAULT '',
    copies_total INTEGER NOT NULL DEFAULT 0,
    copies_left  INTEGER NOT NULL DEFAULT 0,
    state        TEXT NOT NULL,
    pinned       INTEGER NOT NULL DEFAULT 0,
    views        INTEGER NOT NULL DEFAULT 0,
    takes        INTEGER NOT NULL DEFAULT 0,
    left_at      TEXT NOT NULL,
    shelved_at   TEXT,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_shelf
    ON items(library_id, state, shelved_at);
```

The index earns its place: the shelf query and the eviction query both filter by library and state and order by `shelved_at`, and unlike the index removed from `tokens` in Milestone 0, no existing constraint already provides it.

`libraries` gains the §3 fields it lacks:

| Column | Default |
|---|---|
| `slots` | 12 |
| `max_age_days` | 90 |
| `default_copies` | 3 |
| `approval_required` | 1 |
| `steward_contact` | `''` |

`max_age_days` is stored but unused until Milestone 3's expiry sweep. It is added now because it is a §3 library field and adding all five together is one migration rather than two.

`approval_required` **is honored** from this milestone, and is the only one of the five that is. `DESIGN.md` §5 calls the queue steward-configurable and default on: with it on, a left item is `pending` until the CLI decides it; with it off, the leave handler shelves the item immediately by taking the same `ApproveItem` path, so `default_copies` and FIFO eviction apply either way. Anything else here that is stored but inert says so explicitly — a column that reads as a setting and does nothing is worse than one that is documented as waiting, because `docs/shelf-acceptance.md` teaches a steward to edit `libraries` by hand.

`slots` and `default_copies` are read **inside** the approval transaction, from the row, never from a `Library` value a caller passes in. `CreateLibrary` writes none of these five columns — the migration defaults do — so a library struct that never came back from the database carries zeroes, and a zero `slots` would make every approval evict.

---

## 10. Package layout

```
internal/boulevard/item.go    Item, ItemState, ItemType — pure types
internal/store/item.go        library-scoped repository, and the eviction transaction
internal/web/leave.go         GET and POST /b/{slug}/leave
internal/web/item.go          GET /b/{slug}/i/{id}
internal/web/shelf.go         extended: load shelved items
internal/web/templates/       leave.html, left.html, item.html; shelf.html grows
cmd/boulevard/queue.go        queue, approve, reject
```

**Every new store method takes a `boulevard.LibraryID`.** The rule as restated in Milestone 1: no store method infers a current library; resolution boundaries produce one; host-scoped queries take none. Nothing here is a resolution boundary, so everything here takes one.

Validation lives as a pure function in `internal/boulevard`, not in the handler — the same split that keeps `tokens.Validate` testable at its boundaries.

---

## 11. Testing

**Pure.** Validation: a blank note, a whitespace-only note, a link that is not http/https, a link with no host, text at 4,000 runes and at 4,001, a note and an attribution at their limits and one rune past them, an unknown type.

**Store.** Round-trip an item through every state. The eviction transaction at the `slots` boundary: approving into a full shelf sheds exactly the oldest by `shelved_at` and shelves the new one, both or neither. A `pending` item never appears in a shelf query. Two libraries' items never mix.

**Handlers.** `POST /leave` without a session is rejected; with a session for another library, also rejected. `GET /leave` without a session renders the form inert with §6's explanation rather than 404ing. A validation failure re-renders the form with the submitted note still present. A `pending` item does not appear on the shelf. `/i/{id}` increments `views` and 404s for an item belonging to another library.

**CLI.** Prefix resolution, including an ambiguous prefix erroring rather than guessing, and a prefix matching nothing.

**Manual.** Scan a printed card, leave a link and a note, approve it from a terminal, and load the shelf from a browser that has never scanned anything. That last part is the milestone: consumption is global, mutation is local.

---

## 12. Amendments to DESIGN.md

1. **§3, Item `state`** — add `pending`. The listed states have no value for an item in the approval queue that §5 requires.
2. **§3, Presence session** — note that no item stores a session reference, and why: sessions are not swept, so such a reference would be a durable link between items left by one person, which §1 forbids in substance if not in wording.

---

## 13. Deferred, with the milestone that owns each

| Deferred | Owner |
|---|---|
| Image uploads: resize, EXIF/GPS stripping, caps, storage | Its own milestone, before or alongside M3 |
| Taking, copies decrementing, the shed view, re-shelving, expiry | Milestone 3 |
| Per-session leave limits (a counter on the session row) | Milestone 3 |
| Displaying the views/takes counters | Milestone 3, when the contrast means something |
| Video embeds, and any per-provider allowlist | Unscheduled; §3 describes them, nothing builds them yet |
| Steward web admin, pins, settings, the approval queue as a web page | Milestone 4 |
