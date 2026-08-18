# Milestone 4a acceptance

Automated tests cover the store, the handlers, and the CLI. What they cannot
cover is a steward actually running the desk on a phone at the box: setting
a key from a cold start, working the queue and shelf with a thumb, pinning
something and watching a fourth pin get refused, and finding out the hard
way what "approving into an all-pinned shelf" looks like on a screen instead
of in a test assertion.

Every check below is performable on the day it is run.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

Do **not** run `boulevard steward-key` yet — the first checks depend on no
key existing. Scan the current card and, through the leave form, get at
least three `link` items onto the shelf with `boulevard approve`, so the
shelf, the queue, and the shed each have something in them once the run
gets going. Set `slots` small (3) directly in the database for the
all-pinned-refusal check; reaching it with the default 12 would take days.

## Check

- [ ] With `boulevard serve` restarted against the database from Set up —
      still with no key generated — the startup output warns about the
      missing key exactly once, and (since the base URL above is `http://`)
      about the plaintext transport exactly once. Neither warning repeats
      on later requests. Do this check first: every check after this one
      runs `boulevard steward-key` against this database, and once it has,
      there is no "no key set" database left to restart against.
- [ ] Before `boulevard steward-key` has ever been run, every steward route
      404s — the hub, the login form, and a guessed `/i/{id}/approve` —
      rather than showing a login page or a 403.
- [ ] `boulevard steward-key` prints a key once. That key logs in from the
      phone at `/b/{slug}/steward/login`.
- [ ] Running `boulevard steward-key` a second time prints a new key. The
      old key no longer logs in; the new one does.
- [ ] On an idle box (nothing pending, nothing expired or taken-to-zero in
      the shed), the hub says "Nothing needs you." With a pending item and
      a shed item present, the hub instead shows counts for each.
- [ ] From the phone, approve a pending item from the queue and it moves to
      the shelf; reject a different pending item and it does not.
- [ ] Pin a shelved item. The public shelf page shows no take control at
      all on that item — not an inert one, none. Pin two more (three
      total); a fourth pin attempt is refused, and the refusal names the
      three items already pinned.
- [ ] With `slots` set to 3 and all three shelved items pinned, approving a
      fourth pending item is refused, naming the three pins, rather than
      evicting one of them to make room.
- [ ] Remove a (non-pinned) shelved item. It leaves the shelf and appears
      in the shed labelled "you took it down." Re-shelve it from the shed
      and it reappears on the public shelf.
- [ ] On the settings page, change the library's name and save. The page
      re-renders in place (it does not redirect — the name change is not
      worth the risk a redirect-with-a-message pattern would add on an
      authenticated admin surface, per the handler's own doc comment). The
      name changes on the public pages, the URL (`/b/{slug}/...`) does not
      change, and the steward is still logged in on the settings page
      afterwards.
- [ ] Lower `slots` below the current shelved count and save. Nothing is
      removed immediately — the shelf sits over capacity. Approve one more
      pending item and exactly one item (the oldest non-pinned) sheds to
      bring the shelf back toward the new limit.
- [ ] Log out. The steward session ends; loading the hub afterwards
      redirects to the login form rather than showing the hub.

## Run record

**Not yet performed.**
