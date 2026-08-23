package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// runExport writes one library out to its own SQLite file: a file copy, not
// a format (DESIGN.md §10). Same schema in, same schema out, so `boulevard
// serve --db <out>` runs the result unchanged — that is what makes ejecting
// a library from a shared boulevard.db a copy rather than a migration, and
// what makes v2's one-database-per-library layout a no-op rather than a
// data conversion.
func runExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	out := fs.String("out", "", "output database path (required)")
	force := fs.Bool("force", false, "overwrite an existing output file")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "--out is required")
		return exitUsage
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	if _, err := os.Stat(*out); err == nil {
		if !*force {
			fmt.Fprintf(os.Stderr, "  x  %s already exists. Pass --force to overwrite it.\n", *out)
			return exitUsage
		}
		// CopyLibraryTo creates its target through store.Open, which applies
		// the migrations and then would happily write into a file that is
		// already there, producing a file with two libraries' worth of rows
		// (or an opaque UNIQUE constraint error, depending on what was in
		// it). --force must remove the file first, not merely permit
		// CopyLibraryTo to append into it.
		if err := os.Remove(*out); err != nil {
			fmt.Fprintf(os.Stderr, "  x  remove %s: %v\n", *out, err)
			return exitIO
		}
	}

	ctx := context.Background()

	// Counted from the source before the copy, so the numbers describe
	// exactly what CopyLibraryTo is about to write. These three listings are
	// reported individually rather than summed into a total on purpose:
	// CopyLibraryTo copies every row in items, including released (rejected)
	// ones, and no store method lists those — there is deliberately no
	// ReleasedItems, matching every other steward-facing tally in this
	// codebase (the hub in internal/web/steward.go sums the same three).
	// Any total this command computed from only these three calls would
	// therefore understate what the file actually holds, so it names what
	// it can name accurately and asserts nothing about the rest.
	pending, err := s.PendingItems(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	shelved, err := s.ShelvedItems(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	shed, err := s.ShedItems(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	toks, err := s.TokensForLibrary(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	if err := s.CopyLibraryTo(ctx, lib.ID, *out); err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	// A secret is the shelf's write credential (DESIGN.md §4), and this
	// export carries every card's — so the success line names counts and
	// the path only, the same restraint runTokens holds. Naming both things
	// the steward needs to know: what is in the box, and that it stays
	// runnable exactly where it lands.
	fmt.Printf("\n  %s  (%d cards · %d on the shelf, %d waiting, %d in the shed)\n\n"+
		"  It holds every card's secret. Keep it as you keep boulevard.db.\n"+
		"  Run it as-is:  boulevard serve --db %s\n\n",
		*out, len(toks), len(shelved), len(pending), len(shed), *out)
	return exitOK
}
