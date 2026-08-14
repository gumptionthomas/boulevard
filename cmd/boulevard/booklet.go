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

	// Peek at an existing library BEFORE prompting, so a base-URL change is
	// part of what the steward is agreeing to rather than news after the
	// fact. Only open a database that already exists — opening would create
	// the file, and a declined prompt must leave nothing behind.
	if _, err := os.Stat(o.db); err == nil {
		peek, err := store.Open(o.db)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return exitIO
		}
		prior, err := peek.LibraryBySlug(ctx, o.slug)
		peek.Close()
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return exitIO
		}
		if err == nil && prior.BaseURL != o.baseURL {
			warnBaseURLChange(os.Stdout, prior.BaseURL, o.baseURL)
		}
	}

	if !o.yes {
		fmt.Printf("\n  Cards will encode:  %s/s/<token>\n  Browse sign:        %s\n\n", o.baseURL, o.baseURL)
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

	lib, created, err := resolveLibrary(ctx, s, o, rand.Reader, os.Stdout)
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
	if err := os.WriteFile(o.out, pdfBytes, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "  x  write %s: %v\n", o.out, err)
		return exitIO
	}

	verb := "Reprinted"
	if created {
		verb = "Created"
	}
	fmt.Printf("\n  %s %s\n  %s  (%d cards, %s)\n\n  Print it, cut the cards, and scan one before you mount anything.\n\n",
		verb, o.db, o.out, len(toks), lib.Slug)
	return exitOK
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
// twelve tokens on first run. It returns whether the library was created.
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

	periods := tokens.Periods(o.installDate, tokens.PeriodCount)
	toks := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(entropy)
		if err != nil {
			return boulevard.Library{}, false, err
		}
		tokID, err := boulevard.RandomBase32(entropy, boulevard.EntropyBytes)
		if err != nil {
			return boulevard.Library{}, false, err
		}
		toks = append(toks, boulevard.Token{
			ID: tokID, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: p.Index, ValidFrom: p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}
	if err := s.InsertTokens(ctx, lib.ID, toks); err != nil {
		return boulevard.Library{}, false, err
	}
	return lib, true, nil
}
