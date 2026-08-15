package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// resolvePrefix finds the one item whose id starts with prefix.
//
// Nobody types 26 characters at a prompt, so any unique prefix works. An
// ambiguous prefix names every candidate rather than guessing: approving the
// wrong thing because a prefix silently resolved to it is the failure worth
// designing against.
func resolvePrefix(items []boulevard.Item, prefix string) (boulevard.Item, error) {
	p := strings.ToUpper(strings.TrimSpace(prefix))
	if p == "" {
		return boulevard.Item{}, fmt.Errorf("give the first few characters of an item's id")
	}
	var matches []boulevard.Item
	for _, it := range items {
		if strings.HasPrefix(strings.ToUpper(it.ID), p) {
			matches = append(matches, it)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return boulevard.Item{}, fmt.Errorf("nothing waiting starts with %q", prefix)
	default:
		var ids []string
		for _, it := range matches {
			ids = append(ids, it.ID)
		}
		return boulevard.Item{}, fmt.Errorf("%q matches %d items: %s",
			prefix, len(matches), strings.Join(ids, ", "))
	}
}

// queueLibrary opens the database and resolves the single library.
//
// The queue commands are for a steward tending one box. A database holding
// more than one library needs a --slug, which is the same shape GET / takes:
// refuse to guess rather than pick.
func queueLibrary(dbPath, slug string) (*store.Store, boulevard.Library, int) {
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "  x  no database at %s\n     Run `boulevard booklet` first to create one.\n", dbPath)
		return nil, boulevard.Library{}, exitUsage
	}
	s, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return nil, boulevard.Library{}, exitIO
	}
	ctx := context.Background()
	if slug == "" {
		slugs, err := s.LibrarySlugs(ctx)
		if err != nil {
			s.Close()
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return nil, boulevard.Library{}, exitIO
		}
		if len(slugs) != 1 {
			s.Close()
			fmt.Fprintf(os.Stderr, "  x  this database holds %d libraries; name one with --slug\n", len(slugs))
			return nil, boulevard.Library{}, exitUsage
		}
		slug = slugs[0]
	}
	lib, err := s.LibraryBySlug(ctx, slug)
	if err != nil {
		s.Close()
		fmt.Fprintf(os.Stderr, "  x  no library %q in %s\n", slug, dbPath)
		return nil, boulevard.Library{}, exitUsage
	}
	return s, lib, exitOK
}

func runQueue(args []string) int {
	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
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

	pending, err := s.PendingItems(context.Background(), lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	if len(pending) == 0 {
		fmt.Printf("\n  Nothing waiting for %s.\n\n", lib.Name)
		return exitOK
	}

	fmt.Printf("\n  %d waiting for %s\n\n", len(pending), lib.Name)
	for _, it := range pending {
		fmt.Printf("  [%s]  %s\n", strings.ToLower(it.ID[:4]), it.Type)
		fmt.Printf("          %s\n", it.Note)
		fmt.Printf("          %s\n", firstLine(it.Payload))
		if it.Attribution != "" {
			fmt.Printf("          left by %s\n", it.Attribution)
		}
		fmt.Printf("          %s\n\n", it.LeftAt.Local().Format("2 Jan, 3:04 PM"))
	}
	fmt.Printf("  boulevard approve <id>   boulevard reject <id>\n\n")
	return exitOK
}

// firstLine keeps a multi-line text payload from flooding the terminal.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	if len(s) > 70 {
		return s[:70] + " …"
	}
	return s
}

func runApprove(args []string) int { return decide(args, "approve") }
func runReject(args []string) int  { return decide(args, "reject") }

func decide(args []string, verb string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "  x  usage: boulevard %s <id>\n     Run `boulevard queue` to see what is waiting.\n", verb)
		return exitUsage
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	ctx := context.Background()
	pending, err := s.PendingItems(ctx, lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	it, err := resolvePrefix(pending, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitUsage
	}

	if verb == "reject" {
		if err := s.RejectItem(ctx, lib.ID, it.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  x  %v\n", err)
			return exitIO
		}
		fmt.Printf("\n  Released. The row stays, so nothing is lost.\n\n")
		return exitOK
	}

	evicted, err := s.ApproveItem(ctx, lib, it.ID, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	fmt.Printf("\n  Shelved.\n")
	if evicted != "" {
		fmt.Printf("  The shelf was full, so the oldest item moved to the shed.\n")
	}
	fmt.Println()
	return exitOK
}
