# Milestone 2 acceptance

Automated tests cover validation, the store, the handlers and the CLI. What
they cannot cover is the loop working end to end with a real card, a real
phone, and a terminal.

Every check below is performable on the day the booklet is printed.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

## Check

- [x] Loading the shelf without scanning shows "The shelf is empty." and
      "Scan the code at the box to take or leave something."
- [x] Tapping "Leave something" without a session shows the form with the
      submit button disabled and the same explanation — not a 404.
- [x] After scanning the current card, the form's submit button is live.
- [x] Submitting with a blank note refuses, and the link you typed is still
      in the form. Losing a composed note is this form's worst failure.
- [x] Submitting a good link shows "Left at the box." and says a steward
      looks first.
- [x] The shelf does NOT show the item yet.
- [x] `boulevard queue` lists it with a four-character id, the note, and
      when it was left.
- [x] `boulevard approve <id>` says "Shelved."
- [x] The shelf now shows it, note first, with the domain beneath and
      "3 copies".
- [x] Loading the shelf on a phone that has never scanned anything shows the
      item. Consumption is global; mutation is local.
- [x] Tapping the note opens the item's own page.
- [x] `boulevard reject <id>` on a second submission says "Released.", and
      that item never reaches the shelf.
- [x] An ambiguous id prefix refuses and names the candidates rather than
      guessing.

## The full shelf

Approving a thirteenth item sheds the oldest — the default shelf holds 12
(DESIGN.md §3). To see this without leaving thirteen things by hand, create
a second, scratch library with a two-slot shelf.

`boulevard` has no `settings` command yet (that is Milestone 4), and
`CreateLibrary` writes only the six original columns — `slots` is never
part of that call, so a freshly created library keeps the column's default
of 12 regardless. Set it directly, the same way
`docs/presence-acceptance.md` edits `tokens` on a scratch database for its
date-manipulation checks. Give it its own output file and its own port too:
the "Set up" section above already wrote `boulevard-booklet.pdf` in this
directory and, if you have followed the checklist in order, is still
serving on `0.0.0.0:8080` — reusing either would fail before `small.db` is
even created (`booklet` refuses to overwrite an existing PDF without
`--force`, and two servers cannot bind the same port).

    boulevard booklet --name "Small" --location "x" --base-url http://<lan-ip>:8081 --db small.db --out small-booklet.pdf

Then, with no server yet started against `small.db`, open it in a SQLite
client and run:

    UPDATE libraries SET slots = 2;

Start a second server against it, alongside the one already running from
"Set up" — no need to stop that one, the port is different:

    boulevard serve --db small.db --addr 0.0.0.0:8081

`small.db` is its own library with its own booklet — scan one of *its*
cards from `small-booklet.pdf` (port 8081), not one from the first setup,
to get a session for it. Leave three items through the form, then approve
them one at a time, checking `boulevard queue --db small.db` between each:

    boulevard approve --db small.db <id>

- [x] Approving the third sheds the first: the shelf still shows two, and
      the CLI says the oldest moved to the shed.
- [x] The shed item is not on the shelf and not in the queue. There is no
      way to browse it yet — the shed view is Milestone 3.

When finished, delete `small.db` and its `-wal`/`-shm` sidecars — WAL mode
leaves them behind, and they hold token secrets the same as the main
database.

## Run record

**Performed 15 August 2026, against `milestone-2-shelf`.** Booklet printed
on a Brother HL-L2305, cards cut, scanned with a phone over LAN. All
checks in "Check" passed.

"The full shelf" was driven headlessly rather than from the phone: a
token secret was read out of `small.db` and the three leaves were POSTed
with curl. The camera path is not what that section tests, and it was
already proven by the checks above; twelve printed cards for a library
deleted ten minutes later bought nothing. It exercised the same handlers
and the same eviction transaction. Recorded so nobody reads those two
boxes as phone-verified.

    approve #3 -> "Shelved."
                  "The shelf was full, so the oldest item moved to the shed."
    shelf     -> item three, item two.  Item one gone.
    queue     -> "Nothing waiting for Small."

The shed item's own page was checked too, beyond what the boxes ask: it
returns 404 while a shelved sibling returns 200. That is the `handleItem`
state filter, which the whole-branch review had to add — worth confirming
against a running server rather than only in a test.

One defect turned up, fixed on the branch: `.gitignore` covered neither
`*.db` nor `*.pdf`. Token secrets are stored in plaintext by design, so a
stray `git add -A` in a working install would have committed the shelf's
write credential.

The footer contrast fix landed just before this run and was confirmed in
the served CSS.
