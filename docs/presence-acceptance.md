# Milestone 1 acceptance

Automated tests cover validation, sessions and handlers. What they cannot
cover is a real phone scanning real ink. Run this against a printed booklet.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

The base URL must be reachable from the phone, so a LAN address rather than
localhost. Print the booklet and cut the cards.

## Check

Every check here is performable on the day the booklet is printed. Anything
that needs a card in a different period lives in "Behaviour that needs date
manipulation" below — a fresh booklet has one current card and eleven future
ones, and no expired card at all.

- [x] Scanning the current month's card opens the shelf in the phone's browser.
- [x] The banner reads "You're at the box." with a wall-clock deadline.
- [x] The deadline is tomorrow's date at roughly the current time, **in the
      server host's timezone** (spec §10.2 — set the host's clock to the box's
      zone; `TZ=... boulevard serve` is enough to check).
- [x] Closing and reopening the browser keeps the banner (the cookie persists).
- [x] Loading the shelf in a private window shows no banner, and shows
      "Scan the code at the box to take or leave something."
- [x] Scanning the browse sign's QR in a private/incognito window lands on the same shelf,
      with no banner. (Do not reuse the browser tab from item 1; the session cookie from
      that scan persists and makes the banner appear.)
- [x] Scanning the *last* card of the booklet — its window is months away —
      shows "This card isn't in use yet.", names the day it starts working
      **with the year**, and does NOT set a cookie. It must not say the card
      is out of date or that it stopped working.
- [x] Editing one character of a scanned URL shows "That code isn't valid."
      and names no library.
- [x] `/b/<wrong-slug>/` returns 404 rather than falling back to the shelf.

## Run record

Performed 2026-08-14 against a printed booklet, a phone on the same LAN, and a
host on `America/Chicago`. All nine checks above passed.

The expired-card and forward-only checks were not run; both need a card in a
period a fresh booklet does not contain, and both now live in the section below.

## Security property: invalid and revoked are indistinguishable

A valid card can be revoked (marked `revoked` in the database). This and an
invalid card must produce identical responses — no discernible difference in
the page text, HTTP status, or timing. Testing this requires database access and
is covered by the automated test `TestScanRevokedIsIndistinguishableFromUnknown`.
If a revoked card produces a different error message than an unknown one, the
software reveals that a photographed or leaked card was real, allowing targeted
attacks on that library. This property is **not testable by hand with a printed
booklet** and is not included in the checklist above.

## Behaviour that needs date manipulation

**Neither of these can be tested on the day the booklet is printed.** Card periods
run from the install date: card 1 covers the rest of the current month and every
other card is future-dated. So a fresh booklet contains no expired card at all,
and card 2 does not enter its grace window until seven days before the next
calendar month — typically three to four weeks after printing.

Both checks below therefore work on a scratch copy of the database. Set it up once:

1. Copy `boulevard.db` to `test-dates.db`. **Never run these statements on the
   live database** — they rewrite token validity windows.
2. Open `test-dates.db` in SQLite and run both statements:
   ```sql
   -- Cards 2 and 3: valid today, for the forward-only check.
   UPDATE tokens
   SET valid_from = date('now'),
       valid_until = date('now', '+30 days')
   WHERE period_index IN (2, 3);

   -- Card 4: expired well past its grace window, for the out-of-date check.
   UPDATE tokens
   SET valid_from  = date('now', '-45 days'),
       valid_until = date('now', '-15 days')
   WHERE period_index = 4;
   ```
   Card 1 is untouched and keeps validating. Absolute dates are used rather than
   shifts relative to the existing values, so the result does not depend on what
   day of the month the booklet was printed.
3. Run `boulevard serve --db test-dates.db --addr 0.0.0.0:8080`.
4. When you are done with both checks below, delete `test-dates.db` **and its
   `-wal` and `-shm` sidecars** — WAL mode leaves them behind, and they still
   hold token secrets.

Both procedures below were verified end to end on 2026-08-14 by applying the SQL
to a scratch copy and scanning the resulting cards: card 4 rendered "This card is
out of date." with no cookie, and scanning cards 2, 3 then 1 left card 1 `pending`,
card 2 `expired` and card 3 `active` — an older card granting a session without
becoming active, which is the rule.

### An expired card

- [ ] Scanning card 4 shows "This card is out of date.", names the day it stopped
      working, and does NOT set a cookie. The page still names the library and
      offers a link to the shelf — this is diagnostic information for the steward,
      not a failure.

### Forward-only activation

Delete the session cookie, or use a fresh private window, between each scan.

- [ ] Scan card 2. The shelf opens with the banner; card 2 is now the active card.
- [ ] Scan card 3. The shelf opens with the banner; card 3 is now active and card 2
      has been expired.
- [ ] Scan card 1. The shelf still opens and still grants a session — but card 3
      stays active. If card 1 becomes active, the forward-only rule is broken.
