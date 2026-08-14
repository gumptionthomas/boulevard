package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/booklet"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
	"github.com/gumptionthomas/boulevard/internal/version"
)

type bookletOpts struct {
	name, location, baseURL, slug string
	out, db                       string
	skipDNS, yes, force           bool
	installDate                   boulevard.Date
}

func runBooklet(args []string) int {
	fs := flag.NewFlagSet("booklet", flag.ContinueOnError)
	var o bookletOpts
	fs.StringVar(&o.name, "name", "", "library display name (required)")
	fs.StringVar(&o.location, "location", "", "human location label (required)")
	fs.StringVar(&o.baseURL, "base-url", "", "scheme and host, no path (required)")
	fs.StringVar(&o.slug, "slug", "", "URL slug (default: derived from name)")
	fs.StringVar(&o.out, "out", "boulevard-booklet.pdf", "output PDF path")
	fs.StringVar(&o.db, "db", "boulevard.db", "database path")
	fs.BoolVar(&o.skipDNS, "skip-dns", false, "skip the DNS check")
	fs.BoolVar(&o.yes, "yes", false, "skip the confirmation prompt")
	fs.BoolVar(&o.yes, "y", false, "shorthand for --yes")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing output file")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	if o.name == "" || o.location == "" || o.baseURL == "" {
		fmt.Fprintln(os.Stderr, "--name, --location and --base-url are all required")
		return exitUsage
	}

	normalized, host, err := boulevard.ValidateBaseURL(o.baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n     Every card and the permanent browse sign encode this host.\n", err)
		return exitUsage
	}
	o.baseURL = normalized

	if o.slug == "" {
		o.slug = boulevard.Slugify(o.name)
	}
	if o.slug == "" {
		fmt.Fprintf(os.Stderr, "could not derive a slug from name %q; pass --slug\n", o.name)
		return exitUsage
	}
	o.installDate = boulevard.DateFromTime(time.Now())

	// A name too long for the permanent browse sign is a --name problem, so
	// it is caught here with the other usage errors — before the DNS lookup,
	// before the prompt, and above all before a library and twelve tokens
	// are committed. Discovering it at render time left a database behind
	// and no PDF.
	if err := booklet.ValidateSignName(o.name); err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitUsage
	}
	// Likewise a base URL so long that a card's QR would be too dense to
	// scan reliably: a --base-url problem, refused before anything is
	// written rather than after the tokens are minted.
	if err := booklet.ValidateCardPayload(o.baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitUsage
	}

	if !o.skipDNS {
		if err := checkDNS(host); err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n     Fix the URL, or pass --skip-dns if your DNS isn't live yet.\n", err)
			return exitUsage
		}
	}

	if _, err := os.Stat(o.out); err == nil && !o.force {
		fmt.Fprintf(os.Stderr, "  x  %s already exists. Pass --force to overwrite it.\n", o.out)
		return exitUsage
	}

	ctx := context.Background()

	// Look at the database BEFORE prompting, so that what this run is about
	// to do is part of what the steward agrees to rather than news after the
	// fact.
	peek, err := peekDatabase(ctx, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	if peek.found && peek.baseURL != o.baseURL {
		warnBaseURLChange(os.Stdout, peek.baseURL, o.baseURL)
	}
	printIntent(os.Stdout, o, peek)

	if !o.yes {
		if !confirm(os.Stdin, os.Stdout, "The browse sign is meant to be permanent. Print?") {
			fmt.Println("Nothing written.")
			return exitDeclined
		}
	}

	s, err := store.Open(o.db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	defer s.Close()

	// The peek above has already warned about any base-URL change, under
	// exactly the same condition. This is the most consequential warning the
	// tool prints; it must read once.
	lib, created, err := resolveLibrary(ctx, s, o, rand.Reader, io.Discard)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	toks, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	plan, err := booklet.BuildPlan(booklet.Input{
		Library:   lib,
		Tokens:    toks,
		SourceURL: version.RepoURL,
		BuildLine: "boulevard " + version.Version + " (" + version.Commit + ")",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	pdfBytes, err := booklet.Renderer{CreationDate: time.Now()}.Render(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	// Owner-only, like the database: every card in this PDF carries a token
	// secret in its QR, and a secret is the write credential for the shelf.
	// The explicit Chmod covers --force, where WriteFile keeps the mode the
	// existing file already had.
	if err := os.WriteFile(o.out, pdfBytes, store.FileMode); err != nil {
		fmt.Fprintf(os.Stderr, "  x  write %s: %v\n", o.out, err)
		return exitIO
	}
	if err := os.Chmod(o.out, store.FileMode); err != nil {
		fmt.Fprintf(os.Stderr, "  x  restrict %s: %v\n", o.out, err)
		return exitIO
	}

	// The verb belongs to the library, not to the database file: the file is
	// usually opened, not created, and saying "Created boulevard.db" about a
	// database that already existed is exactly the reassurance a steward who
	// mistyped --name must not be given.
	verb := "Reprinted"
	if created {
		verb = "Created"
	}
	fmt.Printf("\n  %s library %s\n  %s  (%d cards)\n  %s\n\n  Print it, cut the cards, and scan one before you mount anything.\n\n",
		verb, lib.Slug, o.out, len(toks), o.db)
	return exitOK
}

// dbPeek is what the database already holds for this run's slug, read
// before the confirmation prompt.
type dbPeek struct {
	found      bool
	baseURL    string
	tokenCount int
	otherSlugs []string // only populated when this slug is not present
}

// peekDatabase reads the state runBooklet needs in order to say what it is
// about to do. It only opens a database that already exists: opening would
// create the file, and a declined prompt must leave nothing behind.
func peekDatabase(ctx context.Context, o bookletOpts) (dbPeek, error) {
	var p dbPeek
	if _, err := os.Stat(o.db); err != nil {
		return p, nil
	}
	s, err := store.Open(o.db)
	if err != nil {
		return dbPeek{}, err
	}
	defer s.Close()

	lib, err := s.LibraryBySlug(ctx, o.slug)
	switch {
	case err == nil:
		p.found, p.baseURL = true, lib.BaseURL
		toks, err := s.TokensForLibrary(ctx, lib.ID)
		if err != nil {
			return dbPeek{}, err
		}
		p.tokenCount = len(toks)

	case errors.Is(err, store.ErrNotFound):
		// A new library inside a database that already holds others is
		// exactly where a typo in --name hides, so name the neighbours.
		if p.otherSlugs, err = s.LibrarySlugs(ctx); err != nil {
			return dbPeek{}, err
		}

	default:
		return dbPeek{}, err
	}
	return p, nil
}

// printIntent states which library this run will touch and what it will do
// to it, before the prompt.
//
// The slug is derived from --name and the lookup is by slug, so "The
// Fairview Boulevard" and "Fairview Boulevard" are two different libraries
// with two different sets of secrets. Showing only the two URLs — as this
// once did — hid a one-character typo at the single moment the steward was
// asked to look at anything.
func printIntent(w io.Writer, o bookletOpts, p dbPeek) {
	switch {
	case p.found && p.tokenCount > 0:
		fmt.Fprintf(w, "\n  Reprinting existing library %q — the same %d cards, unchanged.\n", o.slug, p.tokenCount)
	case p.found:
		fmt.Fprintf(w, "\n  Repairing library %q — it holds no cards, so twelve new secrets will be minted.\n", o.slug)
	default:
		fmt.Fprintf(w, "\n  Creating a new library %q, with twelve new secrets.\n", o.slug)
	}
	fmt.Fprintf(w, "    Name:      %s\n    Location:  %s\n    Cards:     %s/s/<token>\n    Sign:      %s\n",
		o.name, o.location, o.baseURL, o.baseURL)
	if len(p.otherSlugs) > 0 {
		fmt.Fprintf(w, "\n  %s already holds: %s\n"+
			"     If you meant one of those, stop and re-run with its --name or --slug.\n"+
			"     A new library is not a reprint: it mints its own twelve secrets.\n",
			o.db, strings.Join(p.otherSlugs, ", "))
	}
	fmt.Fprintln(w)
}

// checkDNS takes the host ValidateBaseURL already parsed, rather than
// re-parsing the URL and inventing an error branch that cannot fire.
func checkDNS(host string) error {
	if _, err := net.LookupHost(host); err != nil {
		return fmt.Errorf("%s does not resolve (%v)", host, err)
	}
	return nil
}

// warnBaseURLChange states plainly what a new base URL costs. DESIGN.md §10
// calls the mounted browse sign the project's real exposure: the digital
// side ejects cleanly, the screwed-to-the-box artifact does not.
func warnBaseURLChange(w io.Writer, from, to string) {
	fmt.Fprintf(w, "\n  !  Base URL is changing from %s to %s.\n"+
		"     Every card and the permanent browse sign will encode the new host.\n"+
		"     The sign already mounted on the box becomes wrong.\n\n", from, to)
}

// confirm asks a yes/no question defaulting to no. The browse sign is meant
// to be permanent, so a bare Enter must never print.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "  %s [y/N] ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

// resolveLibrary loads the library by slug, creating it and minting its
// twelve tokens on first run. It reports whether a booklet was minted, as
// opposed to reprinted.
//
// Existing tokens are NEVER re-minted. Reprinting must reproduce identical
// cards — that is the whole reason secrets are stored in plaintext.
func resolveLibrary(ctx context.Context, s *store.Store, o bookletOpts, entropy io.Reader, warn io.Writer) (boulevard.Library, bool, error) {
	existing, err := s.LibraryBySlug(ctx, o.slug)
	switch {
	case err == nil:
		if existing.BaseURL != o.baseURL {
			warnBaseURLChange(warn, existing.BaseURL, o.baseURL)
		}
		existing.Name = o.name
		existing.LocationLabel = o.location
		existing.BaseURL = o.baseURL
		if err := s.UpdateLibrary(ctx, existing); err != nil {
			return boulevard.Library{}, false, err
		}
		// A library holding no tokens is the wreckage of an interrupted
		// first run: CreateLibrary commits on its own and InsertTokens is a
		// second transaction, so a Ctrl-C, a full disk or a constraint error
		// between them leaves the row without its booklet. Every later run
		// then took this branch, found nothing, and died in BuildPlan with
		// "needs exactly 12 tokens, got 0" — permanently, with deleting the
		// database the only escape. Mint the missing booklet instead.
		toks, err := s.TokensForLibrary(ctx, existing.ID)
		if err != nil {
			return boulevard.Library{}, false, err
		}
		if len(toks) == 0 {
			if err := mintBooklet(ctx, s, existing.ID, o.installDate, entropy); err != nil {
				return boulevard.Library{}, false, err
			}
			// These secrets are new, so this is a mint, not a reprint.
			return existing, true, nil
		}
		return existing, false, nil

	case errors.Is(err, store.ErrNotFound):
		// fall through to creation

	default:
		return boulevard.Library{}, false, err
	}

	id, err := boulevard.NewLibraryID(entropy)
	if err != nil {
		return boulevard.Library{}, false, err
	}
	lib := boulevard.Library{
		ID: id, Slug: o.slug, Name: o.name,
		LocationLabel: o.location, BaseURL: o.baseURL,
	}
	if err := s.CreateLibrary(ctx, lib); err != nil {
		return boulevard.Library{}, false, err
	}
	if err := mintBooklet(ctx, s, lib.ID, o.installDate, entropy); err != nil {
		return boulevard.Library{}, false, err
	}
	return lib, true, nil
}

// mintBooklet generates a full twelve-period booklet for a library that has
// none, and stores it in one transaction.
func mintBooklet(ctx context.Context, s *store.Store, id boulevard.LibraryID, install boulevard.Date, entropy io.Reader) error {
	periods := tokens.Periods(install, tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(entropy)
		if err != nil {
			return err
		}
		tokID, err := boulevard.RandomBase32(entropy, boulevard.EntropyBytes)
		if err != nil {
			return err
		}
		toks = append(toks, boulevard.Token{
			ID: tokID, LibraryID: id, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	return s.InsertTokens(ctx, id, toks)
}
