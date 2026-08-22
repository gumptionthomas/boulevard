# Milestone 4b — token management and export

**Status:** design agreed 22 August 2026. Supersedes nothing; completes the
Milestone 4 split begun in `2026-08-17-steward-desk-design.md`.

Milestone 4a built the desk — auth, hub, queue, shelf, shed, settings, pins
— everything about *what is on the shelf*. This is the other half:
everything about *the box and its data*. The two share the admin shell and
almost nothing else.

Section numbers cite `DESIGN.md` as it stands on 22 August 2026: §4 presence
and the booklet, §6 surfaces, §8 install, §10 multi-tenancy.

## 1. What this builds

Six token operations and one export.

| Operation | New? | Where the work is |
|---|---|---|
| See the current card | list only | `TokensForLibrary` already exists |
| Download the booklet PDF | new surface | `booklet.Render` already returns bytes |
| Force-activate a pending card | new | one transaction |
| Extend the active card | new | one row |
| Revoke a card | new | one column |
| Rotate — twelve fresh future cards | new | one transaction |
| Export the library as one file | new | `internal/store/export.go` |

### Not in this milestone

- **Import.** §14 lists "import from export file" under Later. An export
  that a `serve` can open needs no importer to be useful.
- **Image bundling.** Image upload is unscheduled; there is nothing to
  bundle.
- **Slug changes**, and the redirect map they would require — a v2 concern
  §10 already anticipates.
- **`boulevard init`.** Milestone 5, and it will compose these commands
  rather than reimplement them, the same way it will compose `steward-key`.

## 2. Files

```
internal/store/token.go        + ForceActivateToken, ExtendToken, RevokeToken, RotatePendingTokens
internal/store/export.go       new — CopyLibraryTo
internal/web/steward_tokens.go new — the tokens page, five POSTs, two downloads
internal/web/steward_export.go new — the export page and its download
cmd/boulevard/tokens.go        new — tokens, force-activate, extend, revoke
cmd/boulevard/export.go        new — export
cmd/boulevard/booklet.go       + a rotate mode
internal/booklet/plan.go       card number comes from position, not period_index
internal/web/templates/steward-tokens.html   new
internal/web/templates/steward-export.html   new
```

`internal/web/steward_tokens.go` is a separate file from
`steward_items.go` rather than an addition to it: the two share no state,
and 4a's file is already the largest in the package.

## 3. The five state transitions

### The constraint that shapes all of them

`tokens.Validate` reads `state` for exactly one value:

```go
if tok.State == boulevard.TokenRevoked {
    return Invalid
}
if today.Before(tok.ValidFrom.AddDays(-grace)) { return NotYet }
if today.After(tok.ValidUntil.AddDays(grace))  { return Expired }
return Granted
```

Everything else is the date window. Three consequences run through this
whole section, and each is counterintuitive enough to be worth stating
before the operations that depend on it:

1. **`revoked` is the only state that stops a scan.** Marking a token
   `expired` changes what the steward sees, not what a scanner gets.
2. **Activation is bookkeeping, not authorisation.** §4 already says as
   much: `state` records which card the steward believes is in the door.
3. **Any operation meant to change whether a card scans must move a date**
   (or set `revoked`). An operation that only writes `state` and expects a
   scanning change is a no-op wearing a confirmation message.

### force-activate

**Purpose (§4):** the steward swapped the card early.

**Effect:** on a `pending` token — set `valid_from = today`, set
`state = active`, and set the currently active token to `expired`.

**Why it rewrites `valid_from`.** Without it the command does nothing. A
September card force-activated on 22 August still answers "this card isn't
in use yet", because 22 August is before `valid_from - 7`. And *inside* the
grace window the command is redundant: a card ≤7 days early already scans
with no command at all. Force-activate is therefore only ever meaningful
beyond the grace window, which is precisely where a date has to move.

**The printed card then disagrees with the database.** A card reading
"Sep 1 – Sep 30" will have a period starting 22 August. This is accepted,
not fixed: the steward has physically put that card in the door, the month
name is how they identify it, and rewriting the label would make the card
in their hand unidentifiable. The confirmation copy says what changed.

**`valid_until` is not moved.** The card ends when it was always going to
end; force-activating lengthens the period at the front only.

**Refused** on any token that is not `pending`, naming the state it found.

### extend

**Purpose (§4):** the booklet is lost and the replacement is not printed
yet.

**Effect:** on the `active` token — set `valid_until` to the last day of the
calendar month *following its current `valid_until`*. Repeatable, and each
call is relative to the value it finds: 31 Aug → 30 Sep → 31 Oct. Nothing
else changes.

**Later periods are deliberately untouched.** Cascading the shift through
the remaining eleven tokens would keep exactly one card valid at a time, and
would make every unswapped printed card a lie — the card reading
"September" would carry October's period. Two cards valid at once is the
lesser problem, and it is one this design already accepts: the 7-day grace
on both ends means adjacent cards already overlap by fourteen days. Because
activation moves forward only (§4), scanning the newer card cleanly retires
the older one.

**Refused** unless the token is `active`.

### revoke

**Purpose (§4):** the sheet was stolen or photographed.

**Effect:** `state = revoked`. Nothing else. This is the one state
`Validate` reads, and §4 requires a revoked secret to produce a response
byte-identical to an unknown one — already implemented and pinned by
`TestScanRevokedIsIndistinguishableFromUnknown`.

**Irreversible for that card.** The secret is burned; there is no un-revoke.
The way forward is force-activate the next card, or rotate.

**Revoking the active card stops the box working** until someone walks to it
with a different card. The confirmation says so plainly rather than
softening it.

**Refused** only if already revoked.

### rotate

**Purpose (§4):** "the steward generates a new booklet."

**Effect:** delete every `pending` token and create twelve new ones — fresh
secrets, whole calendar months, the first beginning the day after the active
token's `valid_until`. The active token is not touched.

**One operation covers both cases §4 describes.** Mid-booklet, the sheet was
compromised and the unprinted secrets are discarded. Past month 12 there are
no pending tokens left and the twelve new periods simply follow the current
one. Either way the steward ends with exactly twelve cards to print and a
live card still in the door.

**Why the active card survives.** The alternative — replacing all twelve —
means an action taken at a keyboard makes the box stop working until someone
physically visits it. Killing the live card stays available as a separate,
deliberate act: revoke.

**If there is no active token** (nothing has been scanned yet), the twelve
new periods begin today, with a short first period to the end of the current
month, exactly as install does.

**Never refused.**

### `period_index` becomes monotonic, and no migration is needed

`tokens` carries `UNIQUE (library_id, period_index)`. Rotate mints twelve
new cards while the active one is still in the door, so it cannot reuse
indices 1–12 — the active card is already sitting on one of them.

**The new cards continue the numbering instead of restarting it.** Rotate
mints from the next multiple of twelve plus one: the first rotate produces
13–24, the second 25–36. Nothing is renumbered, no constraint is touched,
and no schema change is required. Two derived values follow, both exact:

    booklet   = (period_index - 1) / 12 + 1
    card      = (period_index - 1) % 12 + 1

Starting each booklet on a multiple-of-twelve boundary — rather than simply
after the highest index in use — is what keeps that arithmetic true when
rotate discards a partly-used booklet's pending cards.

**The card's printed number comes from its position, not its index.**
`placeCard` currently renders `tok.PeriodIndex` as the card number, which
would print "CARD 13 OF 12" after the first rotate. It takes its position in
the sorted twelve being laid out instead. That is also the more honest
statement: the number on a card is where it sits in the booklet the steward
is holding.

**Why not a `booklet_index` column.** It would model the same thing more
explicitly, but it needs `UNIQUE (library_id, booklet_index, period_index)`,
and SQLite cannot alter a constraint without rebuilding the table. Every
migration in this codebase so far is an `ALTER TABLE ADD COLUMN`; a rebuild
would be the first, for information the existing column already carries.

## 4. Export

### Shape

One SQLite file containing one library. The target is created, the same
`schema.sql` and migrations are applied, and the rows for a single
`library_id` are inserted. Same schema in, same schema out.

    boulevard export --slug fairview --out fairview.db
    boulevard serve --db fairview.db      # runs unchanged

**There is no export format.** §10 is explicit that ejection should be a
file copy rather than a serialisation: no format to version, no lossy
mapping, nothing to keep in sync when a column is added, and no importer
required to make the artifact useful. It also makes v2's move to
one-database-file-per-library a no-op rather than a migration — the file
this emits *is* the file v2 will store natively.

### What travels

| Table | Exported | Why |
|---|---|---|
| `libraries` | yes | including `steward_key_hash` |
| `items` | yes | the shelf, the shed, pins, counts |
| `tokens` | yes | plaintext secrets — the copy must be runnable |
| `sessions` | **no** | live presence credentials |
| `steward_sessions` | **no** | live admin credentials |
| `session_takes` | **no** | the record §1 forbids |

**Durable library state travels; ephemeral presence state does not.**
Copying `sessions` or `steward_sessions` would hand working credentials to
whoever holds the file. Copying `session_takes` would move exactly the
durable "who took what" record that table's swept-with-the-session design
exists to avoid being — the same reasoning that keeps a session id out of
the request log and out of a left item.

**`steward_key_hash` does travel.** §10's story is a host who loses interest
handing out twelve files and dissolving cleanly. Each of those files goes to
the steward whose key that hash already is, and stripping it would lock
every one of them out of their own box at the moment they most need in.

### Handling

The file contains every card's secret, which §4 establishes *is* the
shelf's write credential. Written `0600`, like `boulevard.db` and the PDF.
`--out` refuses an existing path unless `--force`, matching `booklet`.

## 5. Surfaces

### Two new hub rows

§6 lists tokens and export as separate bullets, and they stay separate.

**Tokens** (`/b/{slug}/steward/tokens`) — twelve rows, one per period:
month and year, period dates, state, and whether it has been seen. The
active card is marked. Inline actions per row, each a POST:
force-activate, extend, revoke — shown only where they are legal, so the
page never offers an action that will be refused. Below the list: download
the booklet, and rotate.

**Export** (`/b/{slug}/steward/export`) — the warning and one button.

### The plaintext warning

Both downloads carry every card's secret across the network, and `serve`
terminates no TLS. The download pages warn at the point of download —
naming what is in the file and that the connection is not encrypted —
rather than the capability being withheld.

**Downloads are not gated on an `https://` base URL.** A steward who cannot
reprint a booklet from the desk has lost the thing this milestone is for,
and the self-hosted steward on plain HTTP is exactly who this project is
for. The startup banner already says the same thing about the steward key,
which crosses the same wire on every request.

### Both downloads do bounded work

CLAUDE.md's rule that no handler may block matters here: `SetMaxOpenConns(1)`
serialises every request through one connection, and these two handlers read
a whole library and render twelve QR codes respectively.

Both are bounded **by design rather than by luck**: a shelf is capped at
`slots` items, and a booklet is always exactly twelve cards. Neither grows
with traffic or age. No unbounded variant of either may be added — an
"export all libraries" or a "download every booklet ever generated" would
break this and belongs to the v2 host layer with its own connection pool.

### CLI

```
boulevard tokens         [--db boulevard.db] [--slug SLUG]
boulevard force-activate [--db boulevard.db] [--slug SLUG] <period>
boulevard extend         [--db boulevard.db] [--slug SLUG]
boulevard revoke         [--db boulevard.db] [--slug SLUG] <period>
boulevard export         [--db boulevard.db] [--slug SLUG] --out FILE [--force]
boulevard booklet ... --rotate
```

Both surfaces stay, as they did for the queue and the shed in 4a. `tokens`
prints the same twelve rows the web page shows. Cards are addressed by
period index (1–12), not by id: the steward is holding a card that says
"September", and the booklet numbers them "CARD 9 OF 12".

`booklet` already reprints an existing library unchanged, so the read path
exists; `--rotate` is the write.

## 6. Refusals

Each refusal names the state it found, the way 4a's all-pinned refusal names
the pins — a steward should be able to act on the message without going
looking for what is wrong.

| Operation | Refused when | Message names |
|---|---|---|
| force-activate | token is not `pending` | the state it is in |
| extend | token is not `active` | which token *is* active |
| revoke | token is already `revoked` | that it is already revoked |
| rotate | never | — |
| export | `--out` exists without `--force` | the path |

Web refusals render on the tokens page with the enumerated `?ok=` /
error-code pattern 4a established. **No free-text message parameter**: 4a
had to tear one out, because a free-text notice renders arbitrary prose into
an authenticated admin page and `SameSite=Strict` does not contain a pasted
address-bar URL.

## 7. Testing

**Store, per transition:** the happy path, every refusal, and that
neighbouring tokens are untouched — particularly that `extend` writes one
row and `rotate` leaves the active token's secret unchanged.

**Against `Validate`, not just the row.** The force-activate test must show
a card that returned `NotYet` before the call and `Granted` after, on a day
outside the grace window. Asserting `state == active` would pass while the
feature did nothing — this is the defect the design section exists to
prevent, so the test has to be able to see it.

**Numbering across a rotate.** A test rotates a library whose active card is
mid-booklet, then asserts the new twelve carry indices 13–24 and that the
rendered booklet prints them "CARD 1 OF 12" through "CARD 12 OF 12". The
existing golden-PDF fixture covers the pre-rotate case and must not change.

**Clocks stay injected.** `Validate` takes `today`; extend and rotate derive
calendar boundaries. Tests use a non-UTC clock (`chicago(t)`), because the
store round-trips timestamps through RFC3339 in UTC and a test pinning UTC
on both sides passes while the real thing is a day out.

**Export round trip — the marquee test.** Build a library with items, a
shed, pins and a scanned token; export it; open the export with the store
and assert items and tokens present with all three session tables empty;
then serve the copy and fetch its shelf. That last step is what proves the
"no export format" claim: the artifact is only a file copy if a `serve` can
open it with no translation.

**Web:** every new route 404s before a key exists and redirects without a
session; downloads set `Content-Disposition` and the right content type;
the plaintext warning is present on both download pages; the tokens page
omits illegal actions rather than rendering them inert. (Pinned items hide
their take control for a different reason — there the action does not exist
for anyone. Here it is the *token's state* that makes it illegal, and
showing a refused button would invite a tap that can only fail.)

**CLI:** each command against a seeded database, including refusals and
`--force`.

## 8. Acceptance

A physical run, on a phone, following `docs/steward-acceptance.md`'s
pattern — which now also documents scanning a card off a monitor, so no
printer is needed. The checks that cannot be reached from a terminal:

- Force-activate a card more than 7 days early, then **scan that card** and
  reach the shelf. This is the one check that proves the `valid_from`
  rewrite was necessary.
- Revoke the active card, scan it, and get the generic failure page —
  byte-identical to an unknown secret.
- Rotate, download the PDF on the phone, and confirm the active card's QR
  still scans while a previously-pending card's does not.
- Export from the phone, then `serve` the downloaded file and load its
  shelf.
