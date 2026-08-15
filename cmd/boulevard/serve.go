package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
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
