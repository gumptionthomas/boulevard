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

- [x] Pick a `pending` card whose `valid_from` is more than 7 days from
      today — skip the very next card if it is inside the grace window, and
      go one further out in the booklet — and force-activate it from the
      tokens page. Then **scan that card** (crop and display it, per Set
      up) and reach the shelf. This is
      the one check nothing else in this milestone proves: force-activate
      rewrites `valid_from`, and a card whose period has not started still
      fails validation on `state` alone — if the command only flipped
      `state` to `active`, this scan would land on the "not in use yet"
      diagnostic page instead of the shelf.
- [x] Extend the active card. The tokens page shows the new `valid_until`
      on that row and the next card's dates are unchanged — extend touches
      exactly one row. Extend it a second time and confirm the date moves
      again, relative to where it now sits (end of the *next* month from the
      new date, not the original one).
- [x] Revoke the active card, scan it, and get the generic failure page.
      Compare it side by side with scanning a made-up secret
      (`/s/not-a-real-token`): same status, same body, nothing
      library-specific on either. This is the check that fails quietly if it
      fails at all — a revoked-card page that leaks a name or a shelf link
      is otherwise easy to miss.
- [x] The previous check burned the only active card, so before rotating,
      force-activate a different pending card (still booklet 1) and scan it
      in, the same way as the first check. Then rotate from the tokens
      page. That just-activated card still scans and still reaches the
      shelf — rotation must not have touched it. The tokens page now shows
      twelve new `pending` rows reading "Booklet 2 · Card 1 of 12" through
      "Booklet 2 · Card 12 of 12," and the old pending cards from booklet 1
      are gone.
- [x] Download the booklet PDF from the phone
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
- [x] Force-activate booklet 2's first pending card (its handle is the
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
- [x] From the phone, open the export page and download the export. Copy
      the downloaded `.db` file onto the box (or wherever `boulevard serve`
      will run), then `boulevard serve --db <that file>` and load its
      shelf in a browser. The shelf, and the items on it, must match the
      source library — this is the check that proves export is a real file
      copy and not a lossy summary.
- [x] Load the tokens page and the export page and confirm both show the
      plaintext warning naming what the download carries and that the
      connection is not encrypted, before either download link is followed.
- [x] Every mutation in this run so far went through the tokens or export
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

**Performed 22 August 2026 from an Android phone**, against a server on the
LAN at `http://192.168.4.173:8080`. All nine checks pass. One defect was
found that no check asked for; it is recorded below and is not a check
failure.

Unlike Milestone 4a's first run, this one was done from a phone throughout —
every mutation went through the tokens and export pages, which is the surface
this milestone exists to provide. The terminal was used only to set the box
up, to crop cards out of the booklet PDF, and to verify what a phone cannot
see: that the exported file opens as a database and serves an identical
shelf.

**DHCP had moved the machine** from `.171` to `.173` since the 4a run — the
same failure that killed Milestone 3's printed cards overnight. Nothing was
lost because the card under test is generated per-run rather than reused,
which is the whole argument for the no-printer technique in Set up.

## What the run confirmed that a test could not

**Force-activate genuinely rescues a card the grace window rejects.** Card 3
(1–31 October, forty days out) first produced the "not in use yet" page,
correctly naming **24 September** — `valid_from` minus the seven-day grace,
not the date printed on the card. After force-activating, the same card
scanned through to the shelf. `tokens.Validate` reads `state` for exactly one
value, so an implementation that only flipped `state` to `active` would have
left that second scan on the diagnostic page. Nothing else in this milestone
proves the `valid_from` rewrite was necessary.

**A revoked card is indistinguishable from a made-up one.** Scanned side by
side on the phone, the revoked card's failure page and
`/s/ZZZZZZZZZZZZZZZZZZZZZZZZZZ` were identical — no library name, no
location, no shelf link on either. This is the check that fails silently if
it fails at all: any difference confirms to whoever photographed a card that
the code they captured was real and belongs to this box.

**Rotation does not darken the box.** The card in the door kept scanning
through a rotation that replaced nine pending cards, and the desk asked for
confirmation first — a confirmation that did not exist until the final fix
wave, when the CLI had one and the phone, which is where a steward actually
taps, did not.

**Reprint works on a rotated library.** `boulevard booklet` — no `--rotate` —
produced twelve cards against a fifteen-token library. Until three commits
before this run that command died with "booklet needs exactly 12 tokens, got
13", which is to say the command a steward reaches for *when they have lost
their booklet* was broken by this very milestone.

**The booklet download serves one booklet, never a mix.** With fifteen tokens
present it returned exactly twelve, all booklet 2, none of them the card in
the door. That last part is mildly surprising by design — an older booklet's
cards are already in the door or discarded, so there is nothing left to
reprint — and it is better met here than in a support thread.

**A card cropped from a freshly generated PDF scans to the right token.**
Booklet 2's card 1, cropped at grid position 1 out of a reprint of a rotated
library, resolved to the token just force-activated and reached the shelf.
That chain — newest-booklet selection, position-based card numbering, and the
repaired reprint path — is only verifiable by pointing a camera at it.

**Export is a file copy, not a format.** The `.db` downloaded over HTTP from
the export page opened with a plain `boulevard serve --db` and served a shelf
identical to the source, item for item, with fifteen tokens intact and the
file at mode `0600`. No translation step, no importer.

**No surface printed a secret.** The tokens page, the export page and the
`boulevard tokens` listing were each searched for any 26-character Crockford
base32 string — the shape of both a secret and an id — and all three returned
zero.

## Also confirmed, outside the checklist

A bare domain typed into the leave form on the phone (`2b2t.net`) was stored
as `https://2b2t.net`, and the shelf derived its domain from it correctly.
That change was made the same day, and this is the surface it was built for:
typing `https://` on a phone keyboard, one-handed, outdoors.

## Defect found: force-activate relabels the card

**Open. Not a check failure — no check asked for it.**

Four surfaces derive a card's month label from `ValidFrom`, and
force-activate rewrites `ValidFrom` to today:

- `internal/web/steward_tokens.go:88-89` — the tokens page row
- `internal/booklet/plan.go:95-96` — the printed card
- `internal/web/scan.go:118` — the "not in use yet" page
- `internal/web/templates/steward-confirm-rotate.html:8` — via the same row

So a card printed **October 2026** appeared on the desk as **August 2026**
once force-activated, and the two could not be matched. The CLI listing is
unaffected: it labels by position ("Card 3"), not by month.

**This is what the spec expressly rules out.** Its stated reason for
accepting that force-activate makes the printed card disagree with the
database is that the label is what keeps the card identifiable:

> the steward has physically put that card in the door, and the month name
> is how they identify it, and rewriting the label would make the card in
> their hand unidentifiable

**The worst instance is the rotate confirmation.** It read "The card in the
door — August 2026 — keeps working" while the steward held the September
card. That page exists to interrupt an irreversible action and its only job
is to say which card survives; naming the wrong month defeats it. A steward
would reasonably cancel a correct rotation, or proceed believing a different
card is safe.

**It compounds.** After a revoke and a rotation the page showed two rows both
reading "August 2026" — one honestly (the short install-day first period) and
one only because of this defect — with nothing to tell them apart. The
mislabels are permanent rows, not a transient view.

**The cheap fix does not work.** Labelling from `ValidUntil` survives
force-activate, which never touches it, but breaks under extend, which moves
it by design — confirmed in this run, where the active card went to 30
November. Neither endpoint survives both operations, so the printed label has
to be **stored at mint time**: an `ALTER TABLE ADD COLUMN` in the codebase's
established migration style, set at insert and never modified, with the four
surfaces above reading it instead of deriving one.

## Second defect: plain reprint still demands the base URL

**Open. Lower severity.**

`boulevard booklet` without `--rotate` still requires `--name`, `--location`
and `--base-url`, and on a mismatch it warns and then writes the new value
back. `--rotate` was exempted from those flags during this milestone on the
reasoning that retyping a base URL printed on a mounted sign is how the one
permanent artifact acquires a typo — and reprinting a lost booklet, the more
common operation, was left requiring exactly that. Encountered live during
this run while reprinting.

## Still owed

**Outdoors, in bad light, in gloves.** This run was on a phone but indoors,
at a desk, in good light. The tap targets, the contrast and the confirmation
copy have not been read in the conditions the design names.

**The seen-but-pending rotation path.** The foreign-key fix that lets
rotation survive a pending card carrying a live session was not reachable in
this run — every pending card's window was in the future, so no scan of one
could grant a session. It is covered by
`TestRotateSurvivesAPendingCardThatGrantedASession`, and a run that wants it
live needs a library whose install date puts two card windows open at once.

**The five CLI mutations.** `force-activate`, `extend`, `revoke`,
`booklet --rotate` and `export` were exercised on the desk, not the command
line, so their stdout and stderr were not read for a leaked secret. Only
`tokens` has a test asserting that directly.
