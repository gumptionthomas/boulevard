# Milestone 1 acceptance

Automated tests cover validation, sessions and handlers. What they cannot
cover is a real phone scanning real ink. Run this against a printed booklet.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

The base URL must be reachable from the phone, so a LAN address rather than
localhost. Print the booklet and cut the cards.

## Check

- [ ] Scanning the current month's card opens the shelf in the phone's browser.
- [ ] The banner reads "You're at the box." with a wall-clock deadline.
- [ ] The deadline is tomorrow's date at roughly the current time.
- [ ] Closing and reopening the browser keeps the banner (the cookie persists).
- [ ] Loading the shelf in a private window shows no banner, and shows
      "Scan the code at the box to take or leave something."
- [ ] Scanning the browse sign's QR in a private/incognito window lands on the same shelf,
      with no banner. (Do not reuse the browser tab from item 1; the session cookie from
      that scan persists and makes the banner appear.)
- [ ] Scanning a card whose month has passed by more than a week shows
      "This card is out of date." and does NOT set a cookie.
- [ ] Editing one character of a scanned URL shows "That code isn't valid."
      and names no library.
- [ ] `/b/<wrong-slug>/` returns 404 rather than falling back to the shelf.

## Security property: invalid and revoked are indistinguishable

A valid card can be revoked (marked `revoked` in the database). This and an
invalid card must produce identical responses — no discernible difference in
the page text, HTTP status, or timing. Testing this requires database access and
is covered by the automated test `TestScanRevokedIsIndistinguishableFromUnknown`.
If a revoked card produces a different error message than an unknown one, the
software reveals that a photographed or leaked card was real, allowing targeted
attacks on that library. This property is **not testable by hand with a printed
booklet** and is not included in the checklist above.

## Testing forward-only token activation

**This section cannot be tested on the day the booklet is printed.** Card periods
run from the install date. Card 2 does not enter its grace window until seven days
before the start of the next calendar month — typically about 3-4 weeks after
printing. Card 3 is even later.

To test forward-only activation without waiting a month, use a scratch database:

1. Copy `boulevard.db` to `test-forward-only.db` (never run this on the live database).
2. Open `test-forward-only.db` in SQLite and shift the token periods forward:
   ```sql
   UPDATE tokens SET valid_from = date(valid_from, '+30 days'),
                     valid_until = date(valid_until, '+30 days');
   ```
3. Run `boulevard serve --db test-forward-only.db --addr 0.0.0.0:8080`.
4. Test the sequence:
   - Scan card 2. The banner should appear and show it is active.
   - Delete the session cookie (or use a private window).
   - Scan card 3. The banner should appear, showing card 3 is now active.
   - Delete the session cookie again.
   - Scan card 1. The shelf should open but card 3 should remain active (the banner
     should still indicate the later card). If card 1 becomes active, the forward-only
     rule is broken.
5. Delete `test-forward-only.db` when done.
