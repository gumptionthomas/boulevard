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

- [ ] Taking a link opens it and the count on the shelf drops by one.
- [ ] The shelf now offers "Put it back" on that item, and tapping it
      restores the count and returns the control to "Take".
- [ ] Taking the last copy removes the item from the live part of the
      shelf, and it appears instead in the band below the live items —
      labeled, carrying only the undo control.
- [ ] That band is invisible to a phone that never scanned anything, and
      invisible to a different session that also took the item to zero.
- [ ] After three takes with one scan, the take control on every item goes
      inert and explains why ("You've taken three things with this scan.
      Scan the card again for more.") rather than disappearing.
- [ ] Scanning the card again mints a new session, and its take count
      starts back at zero — three more takes are available.
- [ ] After three leaves with one scan, the leave form goes inert with its
      own explanation, the same way the take control does.
- [ ] Composing a fourth leave against an inert form does not lose the
      typed note — refusal must not eat what was written, the same
      requirement Milestone 2's form already met for a blank note.
- [ ] `boulevard shed` lists items with their reason (evicted, expired, or
      taken) and how long ago, newest first.
- [ ] `boulevard reshelve <id>` puts an item back on the shelf and it
      reappears; `boulevard release <id>` on a different shed item does
      not — it stays off the shelf permanently.
- [ ] `boulevard reshelve` against a shelf that is already full refuses and
      names what is in the way, rather than evicting something to make
      room.
- [ ] A pinned item shows no take control at all — not an inert one, none.
- [ ] An expired item is gone from the shelf listing, and its own item page
      (the URL that was shareable while it was live) no longer serves it
      either.

## The undo edge cases

These are easy to get wrong and worth checking by hand rather than trusting
the automated suite alone, since they involve timing between two actions.

- [ ] Take an item down to its last copy, then undo. The item returns to
      the live shelf with its copy restored — not left in the shed.
- [ ] Take one copy of a multi-copy item, then (via the scratch database)
      push its `shelved_at` back past `max_age_days` so the sweep expires
      it, then undo the take. The item stays in the shed — an undo must
      not reverse an expiry that happened in between.
- [ ] Take an item to zero on a shelf that is otherwise full, approve
      something else into the freed slot, then undo the original take.
      The undo refuses to re-shelve ("The shelf filled up while this was
      gone. It's in the shed — the steward can put it back.") but still
      frees the take: a fourth take is available immediately after, even
      though the item itself stayed in the shed.

## Run record

**This run has not been performed.**
