package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

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
	p := normalizeID(prefix)
	if p == "" {
		return boulevard.Item{}, fmt.Errorf("give the first few characters of an item's id")
	}
	var matches []boulevard.Item
	for _, it := range items {
		if strings.HasPrefix(normalizeID(it.ID), p) {
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

// normalizeID puts a typed prefix and a stored id into the same form.
//
// Crockford's alphabet leaves out I, L, O and U precisely so that a human
// reading an identifier off a screen cannot mistype one into another
// (DESIGN.md §4). Uppercasing alone throws that away: someone who reads 0 as
// O gets "nothing waiting starts with..." for an item that is sitting right
// there. Honoring the reason the alphabet was chosen means folding them the
// way Crockford does — O to zero, I and L to one.
//
// Applied to the stored id too, though a generated id can never contain any
// of them: both sides of a comparison must be in the same form, and relying
// on one side being clean is the kind of assumption that stops holding.
func normalizeID(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'O', 'o':
			return '0'
		case 'I', 'i', 'L', 'l':
			return '1'
		}
		return unicode.ToUpper(r)
	}, strings.TrimSpace(s))
}

// handleWidth is how many leading characters of an id the queue prints.
//
// Four is enough for a shelf of twelve, but a collision at four characters
// is the one case where printing four is actively harmful: resolvePrefix
// correctly refuses to guess and names two 26-character ids, and the steward
// has no way to type a longer prefix for the one they meant because the
// queue never showed them one. So the handle is the shortest width that is
// unique across everything being listed.
func handleWidth(items []boulevard.Item) int {
	const shortest = 4
	longest := shortest
	for _, it := range items {
		if len(it.ID) > longest {
			longest = len(it.ID)
		}
	}
	for w := shortest; w < longest; w++ {
		seen := make(map[string]bool, len(items))
		clash := false
		for _, it := range items {
			h := idPrefix(it.ID, w)
			if seen[h] {
				clash = true
				break
			}
			seen[h] = true
		}
		if !clash {
			return w
		}
	}
	return longest
}

func idPrefix(id string, w int) string {
	if len(id) < w {
		return id
	}
	return id[:w]
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
		// Zero is not "pick one of these": there is no --slug that would
		// work, so pointing at the flag sends the steward looking for a name
		// that does not exist anywhere.
		if len(slugs) == 0 {
			s.Close()
			fmt.Fprintf(os.Stderr, "  x  %s holds no libraries yet.\n     Run `boulevard booklet` to create one.\n", dbPath)
			return nil, boulevard.Library{}, exitUsage
		}
		if len(slugs) > 1 {
			s.Close()
			fmt.Fprintf(os.Stderr, "  x  %s holds %d libraries; name one with --slug: %s\n",
				dbPath, len(slugs), strings.Join(slugs, ", "))
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
	width := handleWidth(pending)
	for _, it := range pending {
		fmt.Printf("  [%s]  %s\n", strings.ToLower(idPrefix(it.ID, width)), it.Type)
		fmt.Printf("          %s\n", displayLine(it.Note))
		fmt.Printf("          %s\n", displayLine(it.Payload))
		if it.Attribution != "" {
			fmt.Printf("          left by %s\n", boulevard.Sanitize(it.Attribution))
		}
		fmt.Printf("          %s\n\n", it.LeftAt.Local().Format("2 Jan, 3:04 PM"))
	}
	fmt.Printf("  boulevard approve <id>   boulevard reject <id>\n\n")
	return exitOK
}

// displayLine renders one field of a stranger's submission as exactly one
// harmless line of terminal output.
//
// Both halves are load-bearing. firstLine bounds it: a note is capped at
// 1,000 runes but not at one line, so without this the queue can be made to
// scroll a real entry off the top of the window. Sanitize defuses it: an
// escape sequence in a note is a cursor instruction, and a hostile leaver
// who can move the cursor can redraw the entry above their own — the steward
// then reads one item and types the handle of another.
//
// Sanitizing after firstLine, not before, so the line break the person
// actually typed still ends the line rather than becoming a space in the
// middle of one.
func displayLine(s string) string {
	return boulevard.Sanitize(firstLine(s))
}

// firstLine keeps a multi-line or very long field from flooding the terminal.
//
// Counted in runes, like the validation layer: slicing bytes splits a
// multi-byte rune and prints a replacement character in place of whatever the
// person wrote.
func firstLine(s string) string {
	const maxRunes = 70
	trimmed := false
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s, trimmed = s[:i], true
	}
	if r := []rune(s); len(r) > maxRunes {
		s, trimmed = string(r[:maxRunes]), true
	}
	if trimmed {
		return s + " …"
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
			return decideExit(err)
		}
		fmt.Printf("\n  Released. The row stays, so nothing is lost.\n\n")
		return exitOK
	}

	evicted, err := s.ApproveItem(ctx, lib.ID, it.ID, time.Now())
	if err != nil {
		if errors.Is(err, store.ErrAllPinned) {
			// err already names the count and the capacity — store.ApproveItem
			// wraps ErrAllPinned as "%d of %d slots, and nothing evictable" —
			// the same shape shed.go uses for ErrShelfFull, so this prints
			// that rather than recomputing it. A count alone does not tell
			// the steward which items are blocking, so PinnedItems follows
			// to name them.
			pinned, pErr := s.PinnedItems(ctx, lib.ID)
			if pErr != nil {
				fmt.Fprintf(os.Stderr, "  x  %v\n", pErr)
				return exitIO
			}
			fmt.Fprintf(os.Stderr, "  x  %v — nothing was approved.\n"+
				"     Unpin something or raise the slot count first.\n", err)
			width := handleWidth(pinned)
			for _, p := range pinned {
				fmt.Fprintf(os.Stderr, "     [%s]  %s\n", strings.ToLower(idPrefix(p.ID, width)), displayLine(p.Note))
			}
			return exitUsage
		}
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return decideExit(err)
	}
	fmt.Printf("\n  Shelved.\n")
	if evicted != "" {
		fmt.Printf("  The shelf was full, so the oldest item moved to the shed.\n")
	}
	fmt.Println()
	return exitOK
}

// decideExit separates the steward's mistake from the machine's.
//
// An id that names nothing, or an item someone already decided, is a
// usage error: retype it, or accept that it is done. Anything else is the
// database failing, which is what exitIO means, and a script that retries
// on one and not the other needs them told apart.
func decideExit(err error) int {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNotPending) {
		return exitUsage
	}
	return exitIO
}
