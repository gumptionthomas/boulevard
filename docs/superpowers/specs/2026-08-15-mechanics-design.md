# Milestone 3 — mechanics

**Scope:** take, undo, session sweeping, expiry, the shed, and per-session rate limits — `DESIGN.md` §3, §4, §5 and §13.

**Status:** design approved 15 August 2026.

---

## 1. What is already built

The build-order line for this milestone reads "copies, take, FIFO eviction, expiry sweep, the shed", but Milestone 2 already shipped two of those:

- `ApproveItem` sets `copies_total = copies_left = default_copies` from the library's settings, read inside the approval transaction (§5).
- The same transaction evicts the oldest non-pinned item when the shelf is full, and correctly handles the case where every item is pinned and nothing is evictable.

This milestone therefore builds **take**, **undo**, **session sweeping**, **expiry**, **the shed as a surface**, and **rate limits**. Copies and FIFO eviction are touched only where take and expiry interact with them.

---

## 2. Session sweeping — a correction to Milestone 1

### What Milestone 1 decided, and why it was wrong

The presence spec recorded:

> | Session expiry | Checked on read, never swept | A background sweeper over a table holding at most a handful of live rows is machinery with no purpose. |

That reasoning answered one question — *does an expired session still grant access?* — correctly. Expiry is checked on read, so an expired row is inert.

It did not weigh the second question: *what does an undeleted session row mean?* A session row that is never removed is a permanent record. Anything keyed to a session inherits that permanence. The consequence surfaced one milestone later as §3's rule that no item may store a session reference — a rule written to work around the premise rather than to fix it. This milestone is the second feature to meet the same wall, which is the usual sign the workaround sits in the wrong place.

The original objection was specifically to a **background** sweeper: a goroutine, shutdown handling, and contention for the single connection that `SetMaxOpenConns(1)` allows. This milestone sweeps expired items lazily on the shelf read path anyway (§5 below). Sessions ride the same pass. No goroutine, no timer, no shutdown path — so the objection no longer applies.

### The rule

Sessions are **swept**. An expired session row is deleted, not merely ignored.

Expiry-on-read stays exactly as it is. A session can be expired but not yet swept — traffic drives the sweep — and the read check is what makes that safe. The two are belt and braces, not alternatives. Do not remove the read check on the grounds that sweeping now exists.

### Store methods

```go
// SweepExpiredSessions deletes every session whose expires_at has passed.
// Host-scoped: it takes no LibraryID because the question is about the host,
// the same reason LibrarySlugs takes none.
func (s *Store) SweepExpiredSessions(ctx context.Context, now time.Time) (int64, error)

// SweepExpiredItems sheds every shelved, non-pinned item in one library whose
// shelved_at is older than that library's max_age_days.
func (s *Store) SweepExpiredItems(ctx context.Context, id boulevard.LibraryID, now time.Time) (int64, error)
```

Both return the number of rows affected, for logging and for tests.

**Both run on every HTML read path that can show an item — the shelf handler and the item handler — before either queries.** The item page is easy to forget and would otherwise serve an expired item at its own URL indefinitely: an unswept item is still `shelved` in the database, and the item handler's state filter would pass it. A shareable link that outlives the shelf listing is precisely the failure this milestone exists to prevent. Milestone 2's review already had to add that state filter once; this is the same gap arriving from a different direction.

### What this unlocks

A link from a session to the items it took is now bounded by the session's own lifetime. §3's objection is to a **durable** link — "a user record by another name" — and a row that cannot outlive twenty-four hours is not that. This is what makes undo (§4) possible.

**Leaves remain unlinked.** §3 forbids linking an item to the session that left it, and that rule stands on its own: a left item is public and permanent, so a link from it would survive the sweep by pointing at durable data from the other side. Leaves are counted with a bare counter. The asymmetry is deliberate — a take is a private act against a shelf, a leave is a public contribution.

---

## 3. Take

### Route and preconditions

```
POST /b/{slug}/i/{id}/take
```

`POST` only. A `GET` take URL would be shareable, prefetchable, and — because the response redirects to a submitted URL — an open redirect wearing the shelf's domain. Requiring `POST` removes all three.

Refuse unless every one of these holds:

| Condition | Response when it fails |
|---|---|
| A valid session for this library | 403, shelf re-rendered with the inert control and the scan explanation |
| Item exists, `state = 'shelved'` | 404 |
| `pinned = 0` | 404 — a pinned item has no take control to press |
| `copies_left > 0` | 409, shelf re-rendered saying the last one is gone |
| Fewer than 3 takes on this session | 403, shelf re-rendered with the limit explanation (§7) |

Every refusal re-renders **the shelf**, anchored at the item, whichever page the control was pressed on. Carrying the origin page through the request would buy a slightly better return for a case that should not happen.

The `copies_left > 0` check looks unreachable — an item that reaches zero sheds in the same transaction, so `shelved` with no copies should not exist. It is reachable one way: a steward who sets `default_copies = 0` gets items approved onto the shelf with nothing to take. The check is a real guard for that, and defence in depth otherwise. Do not remove it as dead code.

A take by a session that already holds a `session_takes` row for this item is a **no-op that still redirects** — not an error. Phones retry, and a duplicate submission must not spend a second copy or produce a failure page.

### The transaction

One transaction:

1. `INSERT INTO session_takes (session_id, item_id, taken_at)`
2. `UPDATE items SET copies_left = copies_left - 1, takes = takes + 1 WHERE id = ?`
3. If `copies_left` reached zero: `state = 'shed'`, `shed_at = now`, `shed_reason = 'taken'`

Step 3 is what §5 means by "at zero, the item moves to the shed."

### Where it redirects

One tap records the take and opens the thing. With no JavaScript, that is a `303` to the item's payload:

| Item type | Redirect target |
|---|---|
| `link`, `video` | The item's `payload`, verbatim |
| `text` | `/b/{slug}/#i-{id}` — the shelf, anchored at the item, since a text item has nothing to open |

The payload is already displayed and clickable on the shelf, so redirecting to it exposes nothing new. It is written to the response as-is; nothing fetches it, per §3.

---

## 4. Undo

### Route

```
POST /b/{slug}/i/{id}/untake
```

Valid for as long as this session's `session_takes` row exists — which is to say, until the session expires and is swept. This is the whole reason §2 was necessary.

### The transaction

One transaction, the mirror of take:

1. `DELETE FROM session_takes WHERE session_id = ? AND item_id = ?` — if it removes no row, the undo is a no-op that redirects, for the same retry reason as take
2. `UPDATE items SET copies_left = copies_left + 1, takes = takes - 1 WHERE id = ?`
3. If the item is `state = 'shed'` **and** `shed_reason = 'taken'`: return it to `shelved`, clear `shed_at` and `shed_reason`

Step 3 checks the reason, not just the state. An item that was shed by expiry or eviction while your take was outstanding must not be silently re-shelved by your undo — the steward, not a passer-by, decides that (§6).

The same guard covers an item the steward **released** out of the shed between your take and your undo: `released` is not `shed`, so step 3 does not fire. The copy is still restored and the row still deleted, which is harmless bookkeeping on an item nothing will ever display again, and it keeps the undo path free of a special case. A steward's release must not be reversible by a stranger.

### The full-shelf case

Between the take that shed an item and the undo that would restore it, other items can be approved onto the shelf. Re-shelving would then exceed `slots`.

**Undo refuses, and says so.** It does not evict something else to make room. A stray tap must not cost a *different* item its place — that would turn one person's misfire into another person's eviction, and eviction is FIFO precisely so that no one's behaviour reorders the shelf. The copy is returned to the item, which stays in the shed for the steward:

> The shelf filled up while this was gone. It's in the shed — the steward can put it back.

The `session_takes` row is still deleted and the copy still restored, so the take no longer counts against the limit.

### Effect on the rate limit

Undo frees a take. The limit counts `session_takes` rows, and the row is gone. This is correct: the limit exists to bound how much one presence can remove from a shelf, and an undone take removed nothing.

---

## 5. Expiry

Items that are `shelved`, not pinned, and whose `shelved_at` is older than the library's `max_age_days` are shed with `shed_reason = 'expired'`.

The sweep runs as a single `UPDATE` on the shelf read path, immediately before the shelf `SELECT`:

```sql
UPDATE items
   SET state = 'shed', shed_at = ?, shed_reason = 'expired'
 WHERE library_id = ? AND state = 'shelved' AND pinned = 0
   AND shelved_at < ?
```

**Why on the read path rather than a goroutine.** It follows Milestone 1's check-on-read precedent, needs no background machinery, and guarantees a visitor can never see an item that should already have shed. On almost every request it touches zero rows, and a shelf holds twelve items by default. `SetMaxOpenConns(1)` means everything serializes through one connection regardless, so a background sweeper would contend with live requests rather than avoid them.

It is not throttled. A throttle would mean a shelf can show an expired item for the length of the throttle window, and would introduce per-process state that a restart resets — something to reason about again in v2's bounded cache of library handles.

**Pinned items never expire**, per §5. The `pinned = 0` predicate is the same one eviction already uses.

---

## 6. The shed

### Why items know how they got there

The shed now fills from three different mechanisms, and a steward deciding whether to re-shelve needs to know which:

| `shed_reason` | Meaning |
|---|---|
| `evicted` | Pushed off a full shelf by a newer item (FIFO) |
| `expired` | Sat longer than `max_age_days` |
| `taken` | Taken to zero copies — people wanted it |

These read very differently. An expired item is stale; an item taken to zero is the opposite. §5 says the shed "keeps the decision human", and a human cannot make that decision without knowing which happened.

**This does not make anything attention-weighted.** Eviction stays FIFO and popularity-blind, `views` still drives nothing, and take counts are already public per §7. The reason is a label on a decision a person makes, not an input to an algorithm. Do not extend it into automatic re-shelving of popular items.

`ApproveItem`'s existing eviction must be updated to set `shed_at` and `shed_reason = 'evicted'`, which it currently does not — it only sets `state`.

### The CLI

Web admin is Milestone 4. This milestone gives the shed the same shape Milestone 2 gave the approval queue:

```
boulevard shed     [--db boulevard.db] [--slug SLUG]
boulevard reshelve [--db boulevard.db] [--slug SLUG] <id>
boulevard release  [--db boulevard.db] [--slug SLUG] <id>
```

Reuse `resolvePrefix` — any unique prefix, case-insensitive, Crockford-folded, refusing ambiguity by naming every candidate — and `boulevard.Sanitize` on every stranger-supplied string that reaches the terminal. Both exist and both are load-bearing: `Sanitize` closes a terminal-injection attack where a note containing `\x1b[1A\x1b[2K` erases the line above it and draws a forged entry, so the steward reads one item and acts on another.

Listing is newest-shed first, showing the handle, type, reason, when it shed, and the note:

```
  3 in the shed for The Fairview Boulevard

  [bpwj]  link    expired 4 days ago
          The pothole on 4th finally got fixed. Ten months.

  [71jb]  link    taken to zero, 2 days ago
          If you have not read this you should. It is short.

  boulevard reshelve <id>   boulevard release <id>
```

`reshelve` returns an item to `shelved`, sets a fresh `shelved_at` — restarting its expiry clock, since a re-shelved item is new to the shelf again — and clears `shed_at` and `shed_reason`. Copies are restored to `copies_total`.

**`reshelve` onto a full shelf refuses**, for the reason undo refuses: the steward asked to add one item, not to evict another. The message names what is in the way.

`release` is the soft delete already built for rejection — state `released`, never shown, never deleted.

---

## 7. Rate limits

§4 sets them: **3 leaves, 3 takes per session.**

| Limit | Counted as |
|---|---|
| Leaves | `sessions.leaves_used`, a bare counter — §3 forbids linking a left item to its session |
| Takes | `SELECT count(*) FROM session_takes WHERE session_id = ?` |

Counting takes from the rows that already exist means there is no second number to keep in sync, and undo decrements it for free by deleting the row.

At the ceiling the control goes **inert with an explanation**, never hidden — the same principle §6 applies to readers with no session. A visitor should understand the rule, not think the feature broke.

> You've taken three things with this scan. Scan the card again for more.

> You've left three things with this scan. Scan the card again for more.

Scanning again mints a new session with fresh counters. That is not a loophole to close: §4 says presence is **attestable, not enforceable**, and that rotation plus rate limits bound the blast radius rather than sealing it. Someone standing at the box may scan again. Do not attempt to make this airtight.

---

## 8. Pins

Milestone 4 sets pins; this milestone honours them.

- Never evicted — already true, `ApproveItem` filters `pinned = 0`
- Never expire — the sweep uses the same predicate
- Cannot be taken — a pinned item renders **no take control at all**, not an inert one. There is no rule to explain; a pin is furniture, not stock.
- No copies — a pinned item shows no copies line

---

## 9. Schema — migration 4

```sql
ALTER TABLE sessions ADD COLUMN leaves_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items    ADD COLUMN shed_at     TEXT;
ALTER TABLE items    ADD COLUMN shed_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS session_takes (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL REFERENCES items(id),
    taken_at   TEXT NOT NULL,
    PRIMARY KEY (session_id, item_id)
);
```

Add these to `migrations` as version 4. **Do not also add them to `schema.sql`** — a fresh database runs both and fails on "duplicate column name". This is written on the `migration` type and has already been the cause of one defect.

`ON DELETE CASCADE` is what makes the sweep delete take rows. It fires because the DSN already sets `_pragma=foreign_keys(1)`; SQLite ignores the clause silently without it. A test must assert the cascade rather than assume it.

The composite primary key gives take lookups their index and makes a duplicate take a constraint violation rather than a second row.

**No `library_id` on `session_takes`.** §10 makes `library_id` first-class on every item, token, session and setting; a join table is none of those. Every query reaches it through `session_id`, which is already library-scoped, and under v2's one-database-per-library both parents live in the same file — so this does not weaken the promise that ejecting a library is a file copy.

---

## 10. Surfaces

| Route | Method | |
|---|---|---|
| `/b/{slug}/i/{id}/take` | POST | Take. Redirects to the payload. |
| `/b/{slug}/i/{id}/untake` | POST | Undo. Redirects to the shelf. |

The shelf and item templates gain the take control in four states:

| State | Renders as |
|---|---|
| No session | `Take` inert · "Scan the code at the box to take or leave something." |
| Session, not yet taken | `Take` live · `N copies` |
| Session, taken by you | `Taken` · `Put it back` · `N copies` |
| Session, at the limit | `Take` inert · "You've taken three things with this scan." |

Pinned items render none of these.

`Cache-Control: no-store` and `Vary: Cookie` already apply to HTML responses and matter more now: the same shelf URL renders four different controls depending on the cookie.

---

## 11. DESIGN.md amendments

This spec deviates from `DESIGN.md`, so `DESIGN.md` is amended to match.

**§3, sessions.** Replace "Sessions are never swept" with the sweep rule, and rewrite the no-link paragraph to distinguish the two cases: a left item never links to a session, because the item is public and permanent; a take links to the session that made it and is deleted with it, which bounds the link to twenty-four hours and makes it not a user record.

**§3, item table.** Add `shed_at` and `shed_reason` to the Item row.

**§4, session mechanics.** State that sessions are swept lazily on the shelf read path, and that expiry is still checked on read.

**§5, taking.** State that take is one tap that records and opens; that it is `POST` only; that it is undoable while the session lasts; and that undo and re-shelve refuse a full shelf rather than evicting.

**§5, the shed.** State that items record how they got there, and that the reason informs a human decision and nothing automatic.

§7 needs no change. Views still drive nothing.

---

## 12. Testing

The suite is fast and every package runs. Beyond ordinary coverage:

- **Non-UTC clock in every test that formats or compares a time.** The store round-trips timestamps through RFC3339 in UTC. A test that pins UTC on both sides of a comparison passes while the real thing is a day out — this shipped once already, as a banner reading "1:25 AM on 16 August" instead of "8:25 PM tomorrow". Run the suite under `TZ=America/Chicago`; CI already does.
- **Expiry on the exact boundary day**, both sides, with an injected clock. `max_age_days` is a day count against a stored timestamp.
- **The cascade.** Insert a session and a take row, sweep, assert the take row is gone. This asserts the pragma is on as much as the schema.
- **The un-shed path**, which is the fiddliest code here: take to zero, undo, assert the item is `shelved` with `copies_left` restored and `shed_at`/`shed_reason` cleared.
- **Undo does not un-shed an item shed for a different reason.** Take one copy of a multi-copy item, expire the item, undo, assert it stays shed.
- **Undo against a full shelf refuses** and leaves the item shed with the copy restored.
- **Duplicate take and duplicate undo are no-ops**, not errors.
- **Rate limits at the boundary:** the third take succeeds, the fourth is refused, an undo makes room for one more.
- **Take is refused for pinned items** even by a session under its limit.

---

## 13. Not in this milestone

Setting pins, the shed as a web page, the steward password, force-activate, extend, revoke, export — all Milestone 4. Image upload and EXIF stripping are unscheduled and appear in no milestone yet.
