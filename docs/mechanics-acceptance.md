# Milestone 3 acceptance

Automated tests cover the store, the handlers, and the CLI. What they cannot
cover is the loop working end to end with a real card, a real phone, and a
terminal — taking something, undoing it, running out of takes, and watching
an item make its way to the shed and back.

Every check below is performable on the day it is run.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

Scan the current card and, through the leave form, get at least one `link`
item onto the shelf with `boulevard approve`. A few checks below need a
second phone (or the same phone with cookies cleared) that has never
scanned anything, and one that scans deliberately late to test expiry —
have a scratch library ready the way `docs/shelf-acceptance.md`'s "full
shelf" section does, with `max_age_days` and `slots` set directly in the
database for the checks that need small numbers.

## Check

- [x] Taking a link opens it and the count on the shelf drops by one.
- [x] The shelf now offers "Put it back" on that item, and tapping it
      restores the count and returns the control to "Take".
- [x] Taking the last copy removes the item from the live part of the
      shelf, and it appears instead in the band below the live items —
      labeled, carrying only the undo control.
- [x] That band is invisible to a phone that never scanned anything, and
      invisible to a different session that also took the item to zero.
- [x] After three takes with one scan, the take control on every item goes
      inert and explains why ("You've taken three things with this scan.
      Scan the card again for more.") rather than disappearing.
- [x] Scanning the card again mints a new session, and its take count
      starts back at zero — three more takes are available.
- [x] After three leaves with one scan, the leave form goes inert with its
      own explanation, the same way the take control does.
- [x] Composing a fourth leave against an inert form does not lose the
      typed note — refusal must not eat what was written, the same
      requirement Milestone 2's form already met for a blank note.
- [x] `boulevard shed` lists items with their reason (evicted, expired, or
      taken) and how long ago, newest first.
- [x] `boulevard reshelve <id>` puts an item back on the shelf and it
      reappears; `boulevard release <id>` on a different shed item does
      not — it stays off the shelf permanently.
- [x] `boulevard reshelve` against a shelf that is already full refuses and
      names what is in the way, rather than evicting something to make
      room.
- [x] A pinned item shows no take control at all — not an inert one, none.
- [x] An expired item is gone from the shelf listing, and its own item page
      (the URL that was shareable while it was live) no longer serves it
      either.

## The undo edge cases

These are easy to get wrong and worth checking by hand rather than trusting
the automated suite alone, since they involve timing between two actions.

- [x] Take an item down to its last copy, then undo. The item returns to
      the live shelf with its copy restored — not left in the shed.
- [x] Take one copy of a multi-copy item, then (via the scratch database)
      push its `shelved_at` back past `max_age_days` so the sweep expires
      it, then undo the take. The item stays in the shed — an undo must
      not reverse an expiry that happened in between.
- [x] Take an item to zero on a shelf that is otherwise full, approve
      something else into the freed slot, then undo the original take.
      The undo refuses to re-shelve ("The shelf filled up while this was
      gone. It's in the shed — the steward can put it back.") but still
      frees the take: a fourth take is available immediately after, even
      though the item itself stayed in the shed.

## Run record

**Performed 17 August 2026, against `milestone-3-mechanics`.** All sixteen
checks pass. Booklet printed on a Brother HL-L2305, cards cut, scanned with
a phone over LAN.

Split of who did what, so nobody reads a tick as more than it is. The phone
checks — take opens the link and the count drops, "Put it back" restores it,
the last copy moves the item into the band, the band is invisible without a
session, both rate limits go inert with their explanations, and a fresh scan
restores the three — were performed by hand on a real phone. The shed CLI,
the full-shelf refusal, expiry, the pinned control and the three undo edge
cases were driven from a terminal against a scratch library with `slots` and
`max_age_days` set small, since reaching those states by hand would take
days.

The composed-note check is the one worth explaining. It passes, but it is no
longer reachable from a phone: the leave form now goes inert on load once
three leaves are spent, so there is no way to compose a fourth and be
refused. It was verified by posting directly — 403, with both the note and
the payload handed back intact and the explanation shown. The GET-inert
behaviour is the better design and the POST path is the safety net behind
it; the requirement stands for the race where the limit is reached between
loading the form and submitting.

## What the run found that no test did

Two defects, both fixed on this branch before merge:

- **The leave form had no way back to the shelf.** Both other secondary
  pages have always carried "Back to the shelf"; this one did not. Harmless
  until Milestone 3, because the submit button was always live and every
  visitor left through the confirmation page — but with the button disabled
  at the rate limit, the page had no exit but the browser's back gesture, on
  a phone, outdoors, one-handed. Now covered by
  `TestLeaveFormAlwaysOffersAWayBack`.
- **The banner asserted a capability the page denied.** It read "You can
  leave or take until…" whenever a session existed, including directly above
  a disabled leave button explaining that three leaves were spent. It now
  names no verbs — "You're at the box. Until [time]." — and `DESIGN.md` §6
  is amended with the reasoning.

Two false alarms during the run, both the harness rather than the software,
recorded so they are not rediscovered: a 500 on the item page came from a
fixture writing `shelved_at` with SQLite's `datetime()` instead of RFC3339,
which `time.Parse` rejects; and a sweep that appeared not to run had simply
never been reached, because the request stopped at the root redirect without
following it to the shelf handler.

One operational note with nothing to do with the code. The base URL was a
LAN IP, and DHCP moved the machine overnight, so the printed cards pointed
at a dead address. `boulevard booklet` reprinted the same library against a
new base URL with its secrets intact, exactly as §4 promises. The lesson is
the one §8 already states about `--base-url`: a DHCP address is a bad thing
to print. An mDNS hostname survived the change and is what the reprint used.
