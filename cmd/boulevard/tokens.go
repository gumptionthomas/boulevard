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
		fmt.Printf("  %s\n", cardLabel(tok))
		fmt.Printf("    %s — %s, %s\n", tok.ValidFrom, tok.ValidUntil, tok.State)
		if tok.FirstSeenAt == nil {
			fmt.Printf("    never scanned\n\n")
		} else {
			fmt.Printf("    first scanned %s\n\n", tok.FirstSeenAt.Local().Format("2 Jan, 3:04 PM"))
		}
	}
	return exitOK
}

// cardLabel names a token the way the steward's printed card does: the
// month is on the paper, but the number is what force-activate, extend and
// revoke take as an argument, so it has to be visible here too.
//
// A library that has never rotated must read exactly as it did before
// rotation existed — every card is booklet 1, and printing "BOOKLET 1 ·"
// on every line would be new noise for no new information. Only a second
// booklet earns the prefix.
func cardLabel(tok boulevard.Token) string {
	booklet := (tok.PeriodIndex-1)/tokens.PeriodCount + 1
	card := (tok.PeriodIndex-1)%tokens.PeriodCount + 1
	if booklet > 1 {
		return fmt.Sprintf("BOOKLET %d · CARD %d", booklet, card)
	}
	return fmt.Sprintf("CARD %d", card)
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
	fmt.Printf("\n  CARD %d is active from today.\n\n", period)
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
	fmt.Printf("\n  CARD %d now runs through %s.\n\n", period, until)
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

	if err := s.RevokeToken(context.Background(), lib.ID, period); err != nil {
		return tokenErrorExit(err, period)
	}
	fmt.Printf("\n  CARD %d is revoked. There is no un-revoke —\n"+
		"  force-activate the next card or rotate the booklet.\n\n", period)
	return exitOK
}

// cardArg parses the one positional argument every token command takes: the
// card's printed number, not its id. A steward reads "CARD 9" off a card in
// hand, not a 26-character identifier, so that is what these commands take.
func cardArg(fs *flag.FlagSet, verb string) (int, int) {
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "  x  usage: boulevard %s <card number>\n"+
			"     Run `boulevard tokens` to see the booklet.\n", verb)
		return 0, exitUsage
	}
	arg := fs.Arg(0)
	period, err := strconv.Atoi(arg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  \"%s\" is not a card number. Cards are numbered 1-12 in the booklet.\n", arg)
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
		fmt.Fprintf(os.Stderr, "  x  no card numbered %d\n", period)
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
