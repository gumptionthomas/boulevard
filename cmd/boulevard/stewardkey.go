package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// runStewardKey is what brings the steward's credential into being.
//
// This is not `init` (DESIGN.md §8) and deliberately does not try to be:
// install has its own promise to meet, and Milestone 5's `init` will call
// this same generate-hash-store sequence rather than have it built twice.
//
// Running it again replaces the key outright and signs out every steward
// session on this library — that is the whole reset story. A lost key is
// not recoverable, and generating another costs nothing, so there is no
// "are you sure" and no prior key to fall back on. The session revocation
// happens inside store.SetStewardKeyHash itself, in the same transaction as
// the key replacement, so this command does not have to remember to do it.
//
// The key is printed to stdout exactly once. Nothing else prints it, writes
// it to a file, or logs it — only its hash, via HashStewardKey, ever
// reaches the database.
func runStewardKey(args []string) int {
	fs := flag.NewFlagSet("steward-key", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	key, err := boulevard.NewStewardKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	if err := s.SetStewardKeyHash(context.Background(), lib.ID, boulevard.HashStewardKey(key)); err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}

	fmt.Printf("\n  Your steward key, shown once:\n\n")
	fmt.Printf("    %s\n\n", key)
	fmt.Printf("  Write it down. It is not recoverable — run this\n  command again to replace it.\n")
	fmt.Printf("  Anyone previously logged in as steward has been signed out.\n\n")
	return exitOK
}
