# Boulevard

A shelf you have to stand at.

Anyone on the internet may read a Boulevard shelf. Only someone who has
physically stood at the box may change what is on it. Presence is the
credential; there are no accounts.

Named for the strip of grass between sidewalk and street, where Little Free
Libraries stand.

## Status

Milestone 0 — the booklet generator. No server yet.

    boulevard booklet --name "The Fairview Boulevard" \
                      --location "4th & Fairview, Minneapolis" \
                      --base-url https://boulevard.example.org

This writes `boulevard.db` and a printable `boulevard-booklet.pdf`: twelve
tear-off monthly cards, a permanent browse sign, and a cover sheet.

Re-running reprints the *same* cards rather than minting new ones.

## Building

    go build -o boulevard ./cmd/boulevard

To stamp version metadata:

    go build -ldflags "\
      -X github.com/gumptionthomas/boulevard/internal/version.Version=$(git describe --tags --always) \
      -X github.com/gumptionthomas/boulevard/internal/version.Commit=$(git rev-parse --short HEAD)" \
      -o boulevard ./cmd/boulevard

## Documentation

- `DESIGN.md` — the v1 specification and source of truth
- `docs/booklet-acceptance.md` — the manual print-and-scan checklist
- `docs/superpowers/specs/` — per-milestone design documents

## License

AGPL-3.0. See `LICENSE`.
