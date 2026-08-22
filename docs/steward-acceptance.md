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

Use a LAN IP, not an mDNS name. A name survives a DHCP lease change and is
the right choice for anything printed, but Android's Chrome does not
reliably resolve `.local` and a phone run dies at DNS before it starts.

**No printer needed.** Crop the current month's card out of the generated
PDF and show it fullscreen; a phone scans it off the monitor:

    pdftoppm -f 1 -l 1 -r 300 -x 225 -y 150 -W 1050 -H 600 -png booklet.pdf card
    eog -f card-1.png

Those offsets are card 1 (top-left of the 2×5 grid) at 300 dpi, from the
constants in `internal/booklet/geometry.go`. The card under test is the real
generated artifact, not a stand-in.

Do **not** run `boulevard steward-key` yet — the first checks depend on no
key existing. Scan the current card and, through the leave form, get at
least three `link` items onto the shelf with `boulevard approve`, so the
shelf, the queue, and the shed each have something in them once the run gets
going. Note that the leave form stops at three items per session; a fresh
scan issues a new session with a clean counter, so restocking the queue
mid-run means scanning again. Set `slots` to 3 on the steward settings page
for the all-pinned-refusal check — reaching it with the default 12 would
take days, and the settings page is a check in its own right, so there is no
reason to edit the database by hand.

## Check

- [x] With `boulevard serve` restarted against the database from Set up —
      still with no key generated — the startup output warns about the
      missing key exactly once, and (since the base URL above is `http://`)
      about the plaintext transport exactly once. Neither warning repeats
      on later requests. Do this check first: every check after this one
      runs `boulevard steward-key` against this database, and once it has,
      there is no "no key set" database left to restart against.
- [x] Before `boulevard steward-key` has ever been run, every steward route
      404s — the hub, the login form, and the pages behind it — rather than
      showing a login page or a 403. Then probe a mutation with the wrong
      verb: `curl -i http://<host>/b/<slug>/steward/i/ABC/approve` (a GET at
      a POST-only route). It must 404 with no `Allow` header, and so must
      the same path under a slug that does not exist. Earlier revisions of
      this checklist named `/i/{id}/approve`, which is not a route at all —
      it lands on the public item page. The real prefix is
      `/b/{slug}/steward/i/{id}/`, and probing the wrong one is how the 405
      recorded below went unnoticed until the phone run.
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

**Performed 20 August 2026 from a terminal, and again 22 August 2026 from a
phone, both against `milestone-4a-steward`.** All twelve checks pass.

The first run was driven by `curl`. It proved the behaviour and said so
plainly: the milestone's own design constraint is a steward working the desk
one-handed, outdoors, in bad light, and a terminal cannot speak to that. The
second run settled it — every check re-performed on an Android phone against
a server on the LAN, by the steward this desk was designed for.

The base URL was `http://192.168.4.171:8080`, a LAN IP. The 20 August run
used the mDNS name `calvin.local` instead, on the reasoning that a name
follows a machine across DHCP lease changes and a number does not — the
lesson from Milestone 3, where the box moved overnight and every printed
card pointed at a dead address. That reasoning is still right for anything
printed. It is wrong for a phone: Android's Chrome does not reliably resolve
`.local`, so a run that used one would have died at DNS before reaching a
single check.

**No printer was involved.** The booklet PDF was generated as usual, and the
current month's card was cropped out of page 1 with `pdftoppm` and displayed
fullscreen on a monitor. The phone scanned it off the glass. This is worth
recording as a permanent testing technique: the artifact under test is the
real generated card, not a stand-in, and it costs nothing to reprint when a
card rotates or a base URL changes.

## What the phone run found that the terminal run did not

**A GET at any POST-only route returned 405, not 404.** Fixed in this branch
(`hideUnmatchedMethods`, `internal/web/routes.go`).

Every steward mutation is POST-only. Method matching happens in the mux,
before any handler runs, so a GET never reached `requireSteward` — the only
thing that enforces §6's "404 until a key exists". The guard sat behind the
door it was meant to guard. On a box with **no steward key set**, a GET
returned `405 Method Not Allowed` with an `Allow: POST` header, confirming
the route was real. The slug did not have to exist either:

    GET /b/no-such-library/steward/i/ABC/approve   405
    GET /b/no-such-library/steward/                404

So the entire admin route shape was enumerable against any Boulevard host,
before a single key had been minted. No auth bypass — POST correctly 404'd,
so nothing was *doable* — but §6 says a fresh install must not advertise a
surface that is not armed, and a 405 advertises more than the 403 that rule
already forbids, since it also confirms the exact verb.

The fix rewrites 405 to 404 in one place rather than duplicating catch-all
routes seven times, drops the `Allow` header, and writes the same body
`http.NotFound` would have, so a hidden mismatch is byte-identical to a path
that never existed. It covers the public POST-only routes (`take`,
`untake`) too, where the disclosure is milder and the reasoning identical.

Two tests pin it, and both were confirmed to fail without the fix — on all
eleven affected routes.

**How it was found is the point.** Not by a probe of the mutation routes —
by a *typo*. The checklist's second item says to try "a guessed
`/i/{id}/approve`", which is not the route's real shape; the URL handed to
the phone was wrong, the phone landed on the public item route instead, and
chasing why it rendered "That code isn't valid." rather than a bare 404
turned up the real route and, with it, the 405. The checklist had been
probing a path that does not exist since Milestone 4a was written. It is
corrected below.

## What the run confirmed that a test could not

**Regenerating the steward key kills live sessions.** The Critical the final
whole-branch review caught. Confirmed here against a real cookie from a real
phone login: rotating the key bounced the live session to the login form,
and the old key was refused. Before that fix, a captured `bl_steward` cookie
would have survived the documented reset path for its full thirty days.

**The all-pinned refusal fires, and reads correctly on a phone.** Three
shelved, all pinned, `slots` at 3, one waiting. The approval was refused,
named all three pins, offered both ways out, evicted nothing, and left the
item pending. `ApproveItem` has carried a comment since Milestone 2 saying
this case could not fire, because nothing set `pinned` until pins existed.

**The hub's empty state is reachable on a stocked shelf.** "Nothing needs
you." with four items shelved — the state the gate fix was about, since the
original test used an item-free fixture and so never saw the bug.

**Pinned items show no take control at all.** Not an inert one. Everywhere
else in this app an unusable control stays visible with an explanation,
because a remote reader should understand the rule rather than think the
feature is broken. Pinning is the deliberate exception: a pinned item is not
unavailable, it is not on offer, and an inert Take button would describe a
rule that does not exist.

**A fresh scan resets the per-session leave limit.** The first session left
three items and was correctly refused a fourth; re-scanning the same card
issued a new session with a clean counter. The counter lives on the session
row, so this is intended — but it had never been exercised across two real
sessions on a phone.

**Renaming the library does not move it.** The name changed to "Winterview
Boulevard" on every public page and in the CLI output; the slug stayed
`bryant-ave-boulevard`. Renaming must never invalidate paper.

**Lowering `slots` sheds nothing until the next approval**, which then
evicted exactly one non-pinned item, labelled "made room" in the shed —
distinct from the "you took it down." a steward's own removal produces.

## Ergonomic observations — none of them defects

**Typing the steward key.** 26 characters of base32 on a phone keyboard with
no autofill. Mitigated by the fact that a session lasts 30 days and is
almost certainly established indoors rather than at the box — nothing about
the desk requires standing at the shelf, which is what separates it from the
presence flow. Worth considering for Milestone 5: have `boulevard
steward-key` print the key as a terminal QR beside the text, so a steward
scans it off their own screen. `go-qrcode` is already a dependency. What
this must *not* become is a login URL with the key embedded — that would put
the credential into browser history, into the referer header on any outbound
link from the desk, and into any log that records paths, which is the exact
thing §4 refuses to do with scan URLs.

**The all-pinned refusal is long.** Each note is capped at 60 runes, so three
pins plus framing runs to roughly 230 characters — four or five lines on a
phone. The length is the deliberate tradeoff for being able to act without
hunting for which three items are pinned. 30 runes each would still identify
them.

**The navigation took acclimatising**, reported by the steward who
commissioned it. The shape is a two-level tree: queue, shelf, shed and
settings each return to the hub, and the hub returns to the public shelf.
That is consistent and it is not a defect, but "still getting used to the
nav" is not a sentence a terminal run can produce. Whether hub-and-spoke is
the right shape is a Milestone 5 question.

**Logout lives only on the hub**, so reaching it from settings or the shed
means going back first. Defensible — the hub is one tap from every page —
and it was noted in the 20 August run as a checklist wording problem. The
phone run confirms it is a mild one, felt but not obstructive.
