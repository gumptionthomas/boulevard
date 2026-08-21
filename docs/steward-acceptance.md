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

- [x] With `boulevard serve` restarted against the database from Set up —
      still with no key generated — the startup output warns about the
      missing key exactly once, and (since the base URL above is `http://`)
      about the plaintext transport exactly once. Neither warning repeats
      on later requests. Do this check first: every check after this one
      runs `boulevard steward-key` against this database, and once it has,
      there is no "no key set" database left to restart against.
- [x] Before `boulevard steward-key` has ever been run, every steward route
      404s — the hub, the login form, and a guessed `/i/{id}/approve` —
      rather than showing a login page or a 403.
- [x] `boulevard steward-key` prints a key once. That key logs in from the
      phone at `/b/{slug}/steward/login`.
- [x] Running `boulevard steward-key` a second time prints a new key. The
      old key no longer logs in; the new one does.
- [x] On an idle box (nothing pending, nothing expired or taken-to-zero in
      the shed), the hub says "Nothing needs you." With a pending item and
      a shed item present, the hub instead shows counts for each.
- [x] From the phone, approve a pending item from the queue and it moves to
      the shelf; reject a different pending item and it does not.
- [x] Pin a shelved item. The public shelf page shows no take control at
      all on that item — not an inert one, none. Pin two more (three
      total); a fourth pin attempt is refused, and the refusal names the
      three items already pinned.
- [x] With `slots` set to 3 and all three shelved items pinned, approving a
      fourth pending item is refused, naming the three pins, rather than
      evicting one of them to make room.
- [x] Remove a (non-pinned) shelved item. It leaves the shelf and appears
      in the shed labelled "you took it down." Re-shelve it from the shed
      and it reappears on the public shelf.
- [x] On the settings page, change the library's name and save. The page
      re-renders in place (it does not redirect — the name change is not
      worth the risk a redirect-with-a-message pattern would add on an
      authenticated admin surface, per the handler's own doc comment). The
      name changes on the public pages, the URL (`/b/{slug}/...`) does not
      change, and the steward is still logged in on the settings page
      afterwards.
- [x] Lower `slots` below the current shelved count and save. Nothing is
      removed immediately — the shelf sits over capacity. Approve one more
      pending item and exactly one item (the oldest non-pinned) sheds to
      bring the shelf back toward the new limit.
- [x] Log out. The steward session ends; loading the hub afterwards
      redirects to the login form rather than showing the hub.

## Run record

**Performed 20 August 2026, against `milestone-4a-steward`.** All twelve
checks pass.

Driven from a terminal against a server on the LAN rather than from a phone.
That is a real limitation and it is recorded rather than glossed: the
milestone's own design constraint is a steward working the desk one-handed,
outdoors, in bad light, and a `curl` session proves the behaviour without
proving the ergonomics. Every check below is about what the software does,
not how it feels to use, and the second half is still owed. What a terminal
run does prove — states a phone cannot reach quickly, like an all-pinned
shelf or a regenerated key against a live session — it proves well.

The base URL was `http://calvin.local:8080`, an mDNS name rather than a LAN
IP. That is the lesson from the Milestone 3 run, where DHCP moved the
machine overnight and every printed card pointed at a dead address. A name
follows the machine across lease changes; a number does not.

## What the run confirmed that a test could not

**Regenerating the steward key kills live sessions.** This was the Critical
the final whole-branch review caught. A session logged in *before*
`boulevard steward-key` ran was bounced to the login form afterwards, and
the old key returned 401. Before that fix, the documented reset path had no
effect on the attack the startup banner warns about — a captured
`bl_steward` cookie would have survived it for thirty days.

**The all-pinned refusal fires and names the pins.** Three shelved items,
all pinned, `slots` at 3, one pending. Approving returned 409 with:

    The shelf is full and every item on it is pinned: "Beta…", "Gamma…",
    "Delta…". Unpin one, or raise the slot count, before approving this.

Nothing was evicted and the pending item stayed pending. `ApproveItem` has
carried a comment since Milestone 2 saying this case could not fire because
nothing set `pinned`; this is the run where it fired.

**The hub's empty state is reachable.** It read "Nothing needs you. / 3
things on the shelf, nothing waiting, nothing in the shed." — on a box with
a *stocked* shelf, which is the state the gate fix was about. It kept its
navigation rows and its logout control, both of which were missing before
the final review.

**Pinned items show no take control at all.** Not an inert one: the public
shelf rendered three pinned items with no control whatsoever, and the one
unpinned item with a live "Take · 3 copies".

**Lowering `slots` sheds nothing immediately.** Setting 2 against 3 shelved
left all three in place and said so on the form. The next approval evicted
exactly one — the only non-pinned item — and carried the enumerated
`?ok=shelved-evicted` code that replaced the free-text notice parameter.

## One thing the run surfaced, not a defect

The logout control lives only on the hub. Reaching it from settings or the
shed means going back to the hub first. That is defensible — the hub is one
tap away from every page — and it is not a defect, but the acceptance item
was written as though logout were reachable from wherever the steward is.
Noted here so the next reader is not surprised.

## Still owed

A run on an actual phone, outdoors. Every prior milestone had a defect that
only a person holding the thing found: mojibake, a banner in the wrong
timezone, a `.gitignore` that would have committed token secrets, a leave
form with no way out, and a banner asserting a capability the page had
already withdrawn. None of those were visible from a terminal either.
