# Milestone 2 acceptance

Automated tests cover validation, the store, the handlers and the CLI. What
they cannot cover is the loop working end to end with a real card, a real
phone, and a terminal.

Every check below is performable on the day the booklet is printed.

## Set up

    boulevard booklet --name "..." --location "..." --base-url http://<your-lan-ip>:8080
    boulevard serve --addr 0.0.0.0:8080

## Check

- [ ] Loading the shelf without scanning shows "The shelf is empty." and
      "Scan the code at the box to take or leave something."
- [ ] Tapping "Leave something" without a session shows the form with the
      submit button disabled and the same explanation — not a 404.
- [ ] After scanning the current card, the form's submit button is live.
- [ ] Submitting with a blank note refuses, and the link you typed is still
      in the form. Losing a composed note is this form's worst failure.
- [ ] Submitting a good link shows "Left at the box." and says a steward
      looks first.
- [ ] The shelf does NOT show the item yet.
- [ ] `boulevard queue` lists it with a four-character id, the note, and
      when it was left.
- [ ] `boulevard approve <id>` says "Shelved."
- [ ] The shelf now shows it, note first, with the domain beneath and
      "3 copies".
- [ ] Loading the shelf on a phone that has never scanned anything shows the
      item. Consumption is global; mutation is local.
- [ ] Tapping the note opens the item's own page.
- [ ] `boulevard reject <id>` on a second submission says "Released.", and
      that item never reaches the shelf.
- [ ] An ambiguous id prefix refuses and names the candidates rather than
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
date-manipulation checks:

    boulevard booklet --name "Small" --location "x" --base-url http://<lan-ip>:8080 --db small.db

Then, with the server not yet running, open `small.db` in a SQLite client
and run:

    UPDATE libraries SET slots = 2;

Start the server against it:

    boulevard serve --db small.db --addr 0.0.0.0:8080

`small.db` is its own library with its own booklet — scan one of *its*
cards, not one from the first setup, to get a session for it. Leave three
items through the form, then approve them one at a time, checking
`boulevard queue --db small.db` between each:

    boulevard approve --db small.db <id>

- [ ] Approving the third sheds the first: the shelf still shows two, and
      the CLI says the oldest moved to the shed.
- [ ] The shed item is not on the shelf and not in the queue. There is no
      way to browse it yet — the shed view is Milestone 3.

When finished, delete `small.db` and its `-wal`/`-shm` sidecars — WAL mode
leaves them behind, and they hold token secrets the same as the main
database.

## Run record

Not yet performed. This checklist requires a printed booklet, a phone on
the same LAN, and a terminal at the box — none of which are available in
this environment. A human must run it against a live install before
Milestone 2 is considered accepted.
