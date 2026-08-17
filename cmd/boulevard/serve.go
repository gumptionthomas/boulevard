package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/web"
)

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	addr := fs.String("addr", ":8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	// Refuse to create a database here. store.Open would happily make an
	// empty one, and serving 404s from a blank database looks like a bug
	// rather than a missing `boulevard booklet` run.
	if _, err := os.Stat(*db); err != nil {
		fmt.Fprintf(os.Stderr, "  x  no database at %s\n     Run `boulevard booklet` first to create one.\n", *db)
		return exitUsage
	}

	st, err := store.Open(*db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	defer st.Close()

	// The read-path sweep (internal/web) only runs when someone loads a
	// shelf or an item, so a box that goes quiet between restarts — most
	// boxes, per DESIGN.md — keeps its expired sessions, and everything
	// linked to them, until traffic arrives. This is the cheap belt for
	// that gap: one sweep at startup bounds the link on a restart even with
	// zero traffic since. It is not a substitute for the read-path sweep,
	// which still has to run for a long-lived process between restarts.
	// Not fatal: a failure here just means the read-path sweep (and the
	// existing expiry-on-read check) still has to do the whole job, the way
	// it did before this existed.
	n, err := st.SweepExpiredSessions(context.Background(), time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  !  startup session sweep failed: %v\n", err)
	}

	// Two things worth a steward's attention before they walk away from the
	// terminal, checked once per library and printed once each — never a
	// background check, this whole binary reads clocks and prints warnings
	// only from the composition root (cmd/boulevard).
	warnStartup(st)

	// Every request serializes through one SQLite connection
	// (SetMaxOpenConns(1), DESIGN.md §2), so a single stalled connection
	// stalls the whole shelf. The timeouts are what keep a phone that walked
	// out of cellular range mid-request from holding that connection open.
	// Generous enough that nothing legitimate here — four small HTML pages —
	// comes close.
	srv := &http.Server{
		Addr:              *addr,
		Handler:           web.New(st, time.Now).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Printf("\n  Boulevard is listening on %s\n  Database: %s\n  Swept %d expired session(s) at startup.\n\n", *addr, *db, n)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	return exitOK
}

// warnStartup prints the two things a steward needs to know before they
// walk away from the terminal, once per library and once each. Neither
// condition is fatal — both are answered by DESIGN.md §6: a fresh install
// with no key runs the shelf fine, it just 404s every steward route until
// `boulevard steward-key` is run, and a plain-http install runs fine too,
// it just carries the key and its cookie in the clear. Refusing to start
// over either was considered and rejected: every localhost test and every
// LAN install would need an override flag, and a flag everyone passes
// reflexively has stopped being a decision.
//
// A failure reading a library here is reported and skipped rather than
// aborting the whole check — one unreadable row should not silence the
// warning for every other library sharing the database.
func warnStartup(st *store.Store) {
	slugs, err := st.LibrarySlugs(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  !  could not list libraries for startup checks: %v\n", err)
		return
	}
	multi := len(slugs) > 1

	for _, slug := range slugs {
		lib, err := st.LibraryBySlug(context.Background(), slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  !  could not load %q for startup checks: %v\n", slug, err)
			continue
		}

		keySet, err := st.StewardKeyIsSet(context.Background(), lib.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  !  could not check the steward key for %q: %v\n", slug, err)
		} else if !keySet {
			if multi {
				fmt.Printf("  No steward key set for %q. Run `boulevard steward-key --slug %s` to enable the admin.\n", slug, slug)
			} else {
				fmt.Printf("  No steward key set. Run `boulevard steward-key` to enable the admin.\n")
			}
		}

		// The steward key and its cookie (DESIGN.md §6) are the one
		// credential whose loss empties a shelf, so a plain-http install
		// gets told once, in the place it is already looking, rather than
		// silently carrying both in the clear.
		if !strings.HasPrefix(lib.BaseURL, "https://") {
			fmt.Printf("  ! The base URL is %s\n    The steward key and its cookie cross the network in the clear.\n    Anyone on this network can read them. Put TLS in front of this\n    box before it is reachable from outside your LAN.\n", lib.BaseURL)
		}
	}
}
