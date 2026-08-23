package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// runTokens lists the booklet: one line per card, with the period, the
// dates, the state, and whether it has been seen.
//
// Secrets are never printed. A secret is the shelf's write credential
// (DESIGN.md §4), and this output is the thing a steward pastes into a
// support thread. To put the secrets in front of someone, print the
// booklet — that artifact is meant to carry them and is written 0600.
func runTokens(args []string) int {
	fs := flag.NewFlagSet("tokens", flag.ContinueOnError)
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

	toks, err := s.TokensForLibrary(context.Background(), lib.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitIO
	}
	if len(toks) == 0 {
		fmt.Printf("\n  No cards minted for %s yet.\n     Run `boulevard booklet` to print a booklet.\n\n", lib.Name)
		return exitOK
	}

	fmt.Printf("\n  %d cards for %s\n\n", len(toks), lib.Name)
	for _, tok := range toks {
		// The handle it prints must be the handle it accepts — the same
		// rule `boulevard queue` follows for item ids (queue.go). The
		// bracketed number is tok.PeriodIndex itself: force-activate,
		// extend and revoke take it back unchanged. cardLabel is a
		// separate, friendlier string for the same card and is never
		// accepted as an argument — see cardLabel's own comment for why
		// the two diverge after a rotation.
		fmt.Printf("  [%d]  %s\n", tok.PeriodIndex, cardLabel(tok.PeriodIndex))
		fmt.Printf("    %s — %s, %s\n", tok.ValidFrom, tok.ValidUntil, tok.State)
		if tok.FirstSeenAt == nil {
			fmt.Printf("    never scanned\n\n")
		} else {
			fmt.Printf("    first scanned %s\n\n", tok.FirstSeenAt.Local().Format("2 Jan, 3:04 PM"))
		}
	}
	return exitOK
}

// cardLabel names a card the way a steward talks about it: which booklet,
// and its position within that booklet — "Card 9", or "Booklet 2 · Card 1"
// once a rotation is in play. It takes the period index rather than a
// printed number because there is no separate stored "printed number" —
// this arithmetic derives it, the same arithmetic internal/booklet/plan.go
// uses to lay a card's position out on the page.
//
// This is deliberately NOT what force-activate, extend and revoke accept.
// A card's position resets to 1-12 every booklet, but tokens.period_index
// is monotonic (DESIGN.md §6: booklet 2 holds 13-24, not 1-12), so once a
// library has rotated, "Card 1" of booklet 2 is period_index 13 — typing
// the on-paper "1" would hit booklet 1's card 1 instead, which is not what
// the steward meant and would not even still be pending. The commands take
// the bracketed handle this file prints beside the label, never the label
// itself.
//
// A library that has never rotated must read exactly as it did before
// rotation existed — every card is booklet 1, and printing "Booklet 1 ·"
// on every line would be new noise for no new information. Only a second
// booklet earns the prefix.
func cardLabel(periodIndex int) string {
	booklet := (periodIndex-1)/tokens.PeriodCount + 1
	card := (periodIndex-1)%tokens.PeriodCount + 1
	if booklet > 1 {
		return fmt.Sprintf("Booklet %d · Card %d", booklet, card)
	}
	return fmt.Sprintf("Card %d", card)
}

func runForceActivate(args []string) int {
	fs := flag.NewFlagSet("force-activate", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	period, code := cardArg(fs, "force-activate")
	if code != exitOK {
		return code
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	today := boulevard.DateFromTime(time.Now())
	if err := s.ForceActivateToken(context.Background(), lib.ID, period, today); err != nil {
		return tokenErrorExit(err, period)
	}
	fmt.Printf("\n  [%d]  %s — active from today.\n\n", period, cardLabel(period))
	return exitOK
}

func runExtend(args []string) int {
	fs := flag.NewFlagSet("extend", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	period, code := cardArg(fs, "extend")
	if code != exitOK {
		return code
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	until, err := s.ExtendToken(context.Background(), lib.ID, period)
	if err != nil {
		return tokenErrorExit(err, period)
	}
	fmt.Printf("\n  [%d]  %s — now runs through %s.\n\n", period, cardLabel(period), until)
	return exitOK
}

func runRevoke(args []string) int {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	db := fs.String("db", "boulevard.db", "database path")
	slug := fs.String("slug", "", "which library, when the database holds more than one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	period, code := cardArg(fs, "revoke")
	if code != exitOK {
		return code
	}

	s, lib, code := queueLibrary(*db, *slug)
	if code != exitOK {
		return code
	}
	defer s.Close()

	// Read the state before the write: revoking the card in the door costs
	// something no other revoke does, and the confirmation owes the steward
	// that sentence (spec §3). Afterwards the state is "revoked" and the
	// difference is gone. A read failure is not fatal here — the revoke
	// itself is what matters, and its own error handling is below.
	wasActive := false
	if all, err := s.TokensForLibrary(context.Background(), lib.ID); err == nil {
		for _, tok := range all {
			if tok.PeriodIndex == period && tok.State == boulevard.TokenActive {
				wasActive = true
			}
		}
	}

	if err := s.RevokeToken(context.Background(), lib.ID, period); err != nil {
		return tokenErrorExit(err, period)
	}
	fmt.Printf("\n  [%d]  %s — revoked. There is no un-revoke —\n"+
		"  force-activate the next card or rotate the booklet.\n", period, cardLabel(period))
	if wasActive {
		fmt.Printf("\n  !  That was the card in the door. Nothing can be left or taken\n" +
			"     at the box until someone puts a different card in it.\n")
	}
	fmt.Println()
	return exitOK
}

// cardArg parses the one positional argument every token command takes: the
// handle `boulevard tokens` prints in brackets beside each card, which is
// that card's raw period_index — not the number printed on the physical
// card, which resets to 1-12 every booklet while the handle keeps counting
// (13, 14, … after the first rotation). See cardLabel's comment for why the
// two are not interchangeable. A steward types what the listing shows them,
// not what they read off the paper in hand.
func cardArg(fs *flag.FlagSet, verb string) (int, int) {
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "  x  usage: boulevard %s <handle>\n"+
			"     Run `boulevard tokens` to see each card's handle.\n", verb)
		return 0, exitUsage
	}
	arg := fs.Arg(0)
	period, err := strconv.Atoi(arg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  \"%s\" is not a handle. Run `boulevard tokens` to see each card's handle.\n", arg)
		return 0, exitUsage
	}
	return period, exitOK
}

// tokenErrorExit maps a store error to an exit code and prints the refusal.
//
// A card that does not exist, or is not in the state the command requires,
// is the steward's mistake — a mistyped number, or a command aimed at the
// wrong card — so both are exitUsage, and the message names the state found
// rather than just "no" so the steward can act without going hunting.
// Anything else is the database failing.
func tokenErrorExit(err error, period int) int {
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "  x  no card at handle %d. Run `boulevard tokens` to see the handles.\n", period)
		return exitUsage
	}
	if errors.Is(err, store.ErrNotPending) || errors.Is(err, store.ErrNotActive) || errors.Is(err, store.ErrAlreadyRevoked) {
		// Each of these errors already names the state the store found —
		// "period %d is %s" or "token is already revoked" — so printing it
		// verbatim is what lets the steward act without going hunting.
		fmt.Fprintf(os.Stderr, "  x  %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(os.Stderr, "  x  %v\n", err)
	return exitIO
}
