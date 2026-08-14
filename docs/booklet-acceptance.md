# Milestone 0 acceptance

DESIGN.md §12: done means printed, cut with scissors, and every card scans.
Automated tests cannot establish any of this. Run the checklist on real paper
before calling the milestone complete.

## Generate

    boulevard booklet \
      --name "The Fairview Boulevard" \
      --location "4th & Fairview, Minneapolis" \
      --base-url https://your-real-host.example.org

## Print

Household printer, US Letter, **scale set to 100% / "actual size"** — not
"fit to page", which silently shrinks every QR and invalidates the test.

## Check

- [ ] Sheet 1 holds ten cards; sheet 2 holds the cover and two cards.
- [ ] A cut along the hairlines separates the cards cleanly with scissors.
- [ ] One horizontal cut frees the cover intact.
- [ ] A card measures 3.5 x 2 inches and fits a standard business-card sleeve.
- [ ] The purpose bar reads SCAN TO LEAVE OR TAKE and is legible at arm's length.
- [ ] The month is readable at a glance while flipping through a loose stack.
- [ ] Every card shows its own month, dates, and "CARD n OF 12".
- [ ] All twelve QR codes scan from a phone at arm's length in ordinary indoor light.
- [ ] Each card's QR resolves to `<base-url>/s/<something>`, and no two cards
      resolve to the same URL.
- [ ] The browse sign's QR resolves to the bare base URL.
- [ ] Held at driver's-seat distance (~2 m), the card QR is *not* comfortably
      scannable. Presence is the credential; a code readable from a car is a bug.
- [ ] The cover footer names a version, a commit, and the source repository.

## If a card fails to scan

Print scaling is the most common cause — check that the PDF was printed at
100% / "actual size" and not "fit to page", which silently shrinks every QR.

If scaling was correct, other causes can be:

- A damaged or creased card (winter on the box does that).
- Poor indoor lighting at scan time.
- The printer laying down too little toner for fine module detail.

**Note:** A base URL too long to print reliably causes `boulevard booklet`
to refuse the generation and exit with an error. If a booklet PDF exists,
URL length is not the problem.
