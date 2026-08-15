package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/store"
)

// runShed lists what has left the shelf without being released: evicted,
// expired, or taken to zero. Nothing here is public — the shelf itself never
// shows the shed — so this listing, and reshelve/release below, are the only
// way a steward sees or acts on it before Milestone 4 gives it a web admin.
func runShed(args []string) int {
	fs := flag.NewFlagSet("shed", flag.ContinueOnError)
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

	items, err := s.ShedItems(context.Background(), lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	if len(items) == 0 {
		fmt.Printf("\n  Nothing in the shed for %s.\n\n", lib.Name)
		return exitOK
	}

	fmt.Printf("\n  %d in the shed for %s\n\n", len(items), lib.Name)
	width := handleWidth(items)
	now := time.Now()
	for _, it := range items {
		fmt.Printf("  [%s]  %s\n", strings.ToLower(idPrefix(it.ID, width)), it.Type)
		fmt.Printf("          %s, %s\n", it.ShedReason.Label(), agoFrom(it.ShedAt, now))
		fmt.Printf("          %s\n\n", displayLine(it.Note))
	}
	fmt.Printf("  boulevard reshelve <id>   boulevard release <id>\n\n")
	return exitOK
}

// agoFrom renders how long ago a shed happened, in the server's local zone.
//
// The store round-trips timestamps through RFC3339 in UTC (DESIGN.md's
// clock invariant), so this converts to Local before doing anything with it
// — comparing or formatting the raw UTC value here would read hours out to
// whoever is standing in a non-UTC timezone.
func agoFrom(t *time.Time, now time.Time) string {
	if t == nil {
		return "shed"
	}
	d := now.Sub(t.Local())
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		return fmt.Sprintf("%d minute%s ago", n, plural(n))
	case d < 24*time.Hour:
		n := int(d / time.Hour)
		return fmt.Sprintf("%d hour%s ago", n, plural(n))
	default:
		n := int(d / (24 * time.Hour))
		return fmt.Sprintf("%d day%s ago", n, plural(n))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func runReshelve(args []string) int { return shedDecide(args, "reshelve") }
func runRelease(args []string) int  { return shedDecide(args, "release") }

func shedDecide(args []string, verb string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "  x  usage: boulevard %s <id>\n     Run `boulevard shed` to see what is there.\n", verb)
		return exitUsage
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	ctx := context.Background()
	shed, err := s.ShedItems(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	it, err := resolvePrefix(shed, fs.Arg(0))
	if err != nil {
		// A mistyped or ambiguous handle is the steward's own terminal
		// telling them what went wrong, not a machine failure — printed
		// where they're already reading, same as the refusals below.
		fmt.Printf("\n  x  %v\n\n", err)
		return exitUsage
	}

	if verb == "release" {
		if err := s.ReleaseItem(ctx, lib.ID, it.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return shedDecideExit(err)
		}
		fmt.Printf("\n  Released. The row stays, so nothing is lost.\n\n")
		return exitOK
	}

	if err := s.ReshelveItem(ctx, lib.ID, it.ID, time.Now()); err != nil {
		if errors.Is(err, store.ErrShelfFull) {
			// err already names the count and the capacity — store.ReshelveItem
			// wraps ErrShelfFull as "shelf has %d of %d slots" — so this prints
			// that rather than recomputing the count and risking it drifting
			// from what actually blocked the write.
			fmt.Printf("\n  x  %v — nothing was moved.\n"+
				"     Reshelve does not evict to make room — approve or reject\n"+
				"     something first, or release this item instead.\n\n", err)
			return exitUsage
		}
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return shedDecideExit(err)
	}
	fmt.Printf("\n  Back on the shelf.\n\n")
	return exitOK
}

// shedDecideExit separates the steward's mistake from the machine's, the
// same way decideExit does for approve/reject: a mistyped or already-decided
// id is a usage error, anything else is the database failing.
func shedDecideExit(err error) int {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNotShed) {
		return exitUsage
	}
	return exitIO
}
