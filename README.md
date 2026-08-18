# Boulevard

A shelf you have to stand at.

Anyone on the internet may read a Boulevard shelf. Only someone who has
physically stood at the box may change what is on it. Presence is the
credential; there are no accounts.

Named for the strip of grass between sidewalk and street, where Little Free
Libraries stand.

## Status

Milestone 4a — the steward's desk. The booklet generator, the HTTP server,
items, taking, and now a web admin for the steward.

    boulevard booklet    --name "..." --location "..." --base-url https://...
    boulevard steward-key                  # mint the steward's admin credential
    boulevard serve       --addr :8080
    boulevard queue                        # what is waiting for approval
    boulevard approve <id>                 # put it on the shelf
    boulevard reject  <id>                 # release it

Someone at the box scans a card, leaves a link or a note, and it waits for
a steward to approve it onto the shelf or reject it; anyone with a live
session can take a shelved item, undoable until the session ends.

`boulevard steward-key` mints the steward's own credential — a 128-bit key,
printed once, stored only as a hash — and is a required setup step before
any admin route works: every steward route 404s until a key exists.
Running the command again replaces the key and signs out every steward
session on that library, which is the reset path for a lost, overheard, or
captured key.

Once a key exists, the steward's desk (login, hub, approval queue, shelf,
shed, settings, pins) is reachable from a phone at `/b/{slug}/steward/`;
`queue`/`approve`/`reject` and their shed-side siblings remain as CLIs for
headless use. Token management (force-activate, extend, revoke, a new
booklet) and export are not built yet — that is Milestone 4b.

`booklet` writes `boulevard.db` and a printable `boulevard-booklet.pdf`.
`serve` puts the shelf on the web: scanning a card grants a 24-hour session,
and the page tells you so.

**`serve` speaks plain HTTP.** It terminates no TLS, so an `https://`
`--base-url` needs a reverse proxy in front of it doing that. Get this right
before printing: the twelve cards are permanent, and a card whose URL cannot
connect is a card that has to be reprinted. To try it on a home network
first, use the LAN address the phone can actually reach —
`--base-url http://<lan-ip>:8080` with `serve --addr 0.0.0.0:8080`, as
`docs/presence-acceptance.md` describes.

Re-running `booklet` reprints the *same* cards rather than minting new ones —
the library is looked up by its slug, which is derived from `--name`, so the
run says which library it is about to touch before it touches it.

Both `boulevard.db` and the PDF are written owner-readable only: the
database holds every token secret in plaintext, and every card in the PDF
carries one in its QR. A secret is the write credential for the shelf, so
anyone who can read either file can leave and take without ever standing at
the box.

## Building

    go build -o boulevard ./cmd/boulevard

To stamp version metadata:

    go build -ldflags "\
      -X github.com/gumptionthomas/boulevard/internal/version.Version=$(git describe --tags --always) \
      -X github.com/gumptionthomas/boulevard/internal/version.Commit=$(git rev-parse --short HEAD)" \
      -o boulevard ./cmd/boulevard

## Documentation

- `DESIGN.md` — the v1 specification and source of truth
- `docs/booklet-acceptance.md` — Milestone 0 manual print-and-scan checklist
- `docs/presence-acceptance.md` — Milestone 1 manual acceptance checklist
- `docs/shelf-acceptance.md` — Milestone 2 manual acceptance checklist
- `docs/mechanics-acceptance.md` — Milestone 3 manual acceptance checklist
- `docs/steward-acceptance.md` — Milestone 4a manual acceptance checklist
- `docs/superpowers/specs/` — per-milestone design documents

## License

AGPL-3.0. See `LICENSE`.
