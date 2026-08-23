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

Through the leave form on that same scan, leave a `link` item and
`boulevard approve` it onto the shelf (or approve it from the queue page),
the same way `docs/steward-acceptance.md`'s Set up stocks the shelf before
its own checks begin. The export check below copies this library to a
second file and loads its shelf; starting from an empty shelf would make
that comparison pass whether or not the copy actually carries anything, so
one shelved item is what makes it a real check rather than a comparison of
two empty shelves.

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
      force-activate a different pending card (still booklet 1) and scan it
      in, the same way as the first check. Then rotate from the tokens
      page. That just-activated card still scans and still reaches the
      shelf — rotation must not have touched it. The tokens page now shows
      twelve new `pending` rows reading "Booklet 2 · Card 1 of 12" through
      "Booklet 2 · Card 12 of 12," and the old pending cards from booklet 1
      are gone.
- [ ] Download the booklet PDF from the phone
      (`/b/<slug>/steward/booklet.pdf`) **right now, before force-activating
      anything else.** The library holds thirteen-plus tokens at this
      point (the booklet-1 card still in the door, plus booklet 2's twelve
      pending), and the download always serves the *highest-numbered*
      complete booklet — booklet 2 here — never a mix of two. The download
      still succeeds and still has exactly twelve cards, but note which
      twelve: all booklet 2, none of them the card currently in the door.
      Confirm that directly — the period dates on the card in the door (from
      the previous check) do not match any of the twelve in this PDF. This
      step exists to make that fact visible on purpose, rather than
      surfacing later as "I downloaded the booklet and my card isn't in
      it." It would fail this check if the PDF ever contained the active
      card's period, or contained anything other than exactly twelve
      booklet-2 cards.
- [ ] Force-activate booklet 2's first pending card (its handle is the
      lowest of the twelve new rows — read it from the tokens page rather
      than assuming a number) and scan it in. Download the booklet PDF
      again. It is unchanged in shape — still twelve cards, still all
      booklet 2 — but this time the active card *is* among them: it is
      booklet 2's own card 1, the same position cropped in Set Up. Crop
      position 1 out of this new PDF with the same `pdftoppm` command (a
      fresh download is a new file, but the 2×5 layout and its offsets do
      not change) and scan it: it must resolve to the card just
      force-activated and reach the shelf. If this fails, but scanning the
      card straight off the tokens-page force-activate confirmation
      succeeds, the defect is in how the booklet is laid out or selected
      for download, not in force-activate itself.
- [ ] From the phone, open the export page and download the export. Copy
      the downloaded `.db` file onto the box (or wherever `boulevard serve`
      will run), then `boulevard serve --db <that file>` and load its
      shelf in a browser. The shelf, and the items on it, must match the
      source library — this is the check that proves export is a real file
      copy and not a lossy summary.
- [ ] Load the tokens page and the export page and confirm both show the
      plaintext warning naming what the download carries and that the
      connection is not encrypted, before either download link is followed.
- [ ] Every mutation in this run so far went through the tokens or export
      page — that is the surface these checks exist to prove, and is what a
      steward without a terminal open would use. Scroll back through the
      tokens page and the export page as they rendered at every check above
      and confirm neither ever showed a token secret: not in a row, not in
      a confirmation notice, not in a download link's URL. Then, from a
      terminal against this same database, run `boulevard tokens` and
      confirm its listing also shows only handles, labels, dates and
      state — the same restraint `TestTokensListsTheBooklet`
      (`cmd/boulevard/tokens_test.go`) pins for it. This does not exercise
      `force-activate`, `extend`, `revoke`, `booklet --rotate` or `export`
      on the command line, since nothing above ran them there; a run that
      wants that coverage too has to invoke each by hand against a scratch
      database and read its stdout and stderr for a secret, the way
      `TestTokensListsTheBooklet` does for `tokens` — as of this writing
      that is the only one of the six with a test asserting it directly.
      This check fails if the tokens page, the export page, or the
      `boulevard tokens` listing shows a value from the `secret` column
      anywhere.

## Run record

## Still owed
