# Milestone 4b acceptance

Automated tests cover the store, the handlers, and the CLI. What they cannot
cover is a real card scanning off a real screen: force-activating a card the
grace window would otherwise reject, watching a revoked secret produce the
same failure page as a made-up one, and rotating a live booklet without the
card in the door ever going dark. This milestone's checks need a scannable
card more than any before it — most of them are pointless without one.

Every check below is performable on the day it is run.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard steward-key
    boulevard serve --addr 0.0.0.0:8080

Use a LAN IP, not an mDNS name. A name survives a DHCP lease change and is
the right choice for anything printed, but Android's Chrome does not
reliably resolve `.local` and a phone run dies at DNS before it starts.

Scan the current card once through the normal flow so the library has an
active token before any of the checks below touch it — force-activate,
extend and rotate all key off "the active card," and there is nothing to
extend or rotate around on a library that has never been scanned.

**No printer needed.** Crop the current month's card out of the generated
PDF and show it fullscreen; a phone scans it off the monitor:

    pdftoppm -f 1 -l 1 -r 300 -x 225 -y 150 -W 1050 -H 600 -png booklet.pdf card
    eog -f card-1.png

Those offsets are card 1 (top-left of the 2×5 grid) at 300 dpi, from the
constants in `internal/booklet/geometry.go`. The card under test is the real
generated artifact, not a stand-in. Re-run the crop whenever the card that
needs testing is not card 1 — after force-activate or rotate, the card in
the door may be a different position in a different PDF, and the offsets
above only crop position 1. Downloading the booklet from the desk
(`/b/<slug>/steward/booklet.pdf`) produces the same PDF this technique crops
from, so a re-crop after a download is the same command against the new
file.

## Check

- [ ] Pick a `pending` card whose `valid_from` is more than 7 days from
      today — skip the very next card if it is inside the grace window, and
      go one further out in the booklet — and force-activate it from the
      tokens page. Then **scan that card** (crop and display it, per Set
      up) and reach the shelf. This is
      the one check nothing else in this milestone proves: force-activate
      rewrites `valid_from`, and a card whose period has not started still
      fails validation on `state` alone — if the command only flipped
      `state` to `active`, this scan would land on the "not in use yet"
      diagnostic page instead of the shelf.
- [ ] Extend the active card. The tokens page shows the new `valid_until`
      on that row and the next card's dates are unchanged — extend touches
      exactly one row. Extend it a second time and confirm the date moves
      again, relative to where it now sits (end of the *next* month from the
      new date, not the original one).
- [ ] Revoke the active card, scan it, and get the generic failure page.
      Compare it side by side with scanning a made-up secret
      (`/s/not-a-real-token`): same status, same body, nothing
      library-specific on either. This is the check that fails quietly if it
      fails at all — a revoked-card page that leaks a name or a shelf link
      is otherwise easy to miss.
- [ ] The previous check burned the only active card, so before rotating,
      force-activate a different pending card and scan it in, the same way
      as the first check. Then rotate from the tokens page. That
      just-activated card still scans and still reaches the shelf —
      rotation must not have touched it. The tokens page now shows twelve
      new `pending` rows reading "Booklet 2 · Card 1 of 12" through
      "Booklet 2 · Card 12 of 12," and the old pending cards from booklet 1
      are gone.
- [ ] Download the booklet PDF from the phone
      (`/b/<slug>/steward/booklet.pdf`). It has twelve cards, numbered
      1–12. Crop the card matching the one
      currently in the door out of this new PDF and scan it — the QR still
      resolves to the same secret, so it should behave exactly as the
      original crop did.
- [ ] From the phone, open the export page and download the export. Copy
      the downloaded `.db` file onto the box (or wherever `boulevard serve`
      will run), then `boulevard serve --db <that file>` and load its
      shelf in a browser. The shelf, and the items on it, must match the
      source library — this is the check that proves export is a real file
      copy and not a lossy summary.
- [ ] Load the tokens page and the export page and confirm both show the
      plaintext warning naming what the download carries and that the
      connection is not encrypted, before either download link is followed.
- [ ] Read back every response produced during this run — CLI stdout and
      stderr for `tokens`, `force-activate`, `extend`, `revoke`,
      `booklet --rotate` and `export`, plus the tokens and export pages —
      and confirm none of them print a token secret. `boulevard tokens`
      output and the tokens page both show state and dates only.

## Run record

## Still owed
