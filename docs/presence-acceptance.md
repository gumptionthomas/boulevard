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
- [ ] Scanning the browse sign's QR lands on the same shelf, with no banner.
- [ ] Scanning a card whose month has passed by more than a week shows
      "This card is out of date." and does NOT set a cookie.
- [ ] Editing one character of a scanned URL shows "That code isn't valid."
      and names no library.
- [ ] `/b/<wrong-slug>/` returns 404 rather than falling back to the shelf.

## Scanning a future card

Scanning next month's card early should work — the grace window opens seven
days before the period starts. Scanning card 3 while card 2 is active should
make card 3 active and expire card 2. Scanning card 1 afterwards should still
open the shelf but leave card 3 active.
