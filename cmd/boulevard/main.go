package main

import (
	"fmt"
	"os"
)

// Exit codes, per spec §9.2.
const (
	exitOK       = 0
	exitDeclined = 1
	exitUsage    = 2
	exitIO       = 3
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	switch os.Args[1] {
	case "booklet":
		os.Exit(runBooklet(os.Args[2:]))
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "queue":
		os.Exit(runQueue(os.Args[2:]))
	case "approve":
		os.Exit(runApprove(os.Args[2:]))
	case "reject":
		os.Exit(runReject(os.Args[2:]))
	case "shed":
		os.Exit(runShed(os.Args[2:]))
	case "reshelve":
		os.Exit(runReshelve(os.Args[2:]))
	case "release":
		os.Exit(runRelease(os.Args[2:]))
	case "steward-key":
		os.Exit(runStewardKey(os.Args[2:]))
	case "tokens":
		os.Exit(runTokens(os.Args[2:]))
	case "force-activate":
		os.Exit(runForceActivate(os.Args[2:]))
	case "extend":
		os.Exit(runExtend(os.Args[2:]))
	case "revoke":
		os.Exit(runRevoke(os.Args[2:]))
	case "export":
		os.Exit(runExport(os.Args[2:]))
	case "version":
		os.Exit(runVersion())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `boulevard — a shelf you have to stand at

Usage:
  boulevard booklet --name NAME --location LABEL --base-url URL [flags]
  boulevard serve [--db PATH] [--addr ADDR]
  boulevard queue   [--db PATH] [--slug SLUG]
  boulevard approve [--db PATH] [--slug SLUG] <id>
  boulevard reject  [--db PATH] [--slug SLUG] <id>
  boulevard shed     [--db PATH] [--slug SLUG]
  boulevard reshelve [--db PATH] [--slug SLUG] <id>
  boulevard release  [--db PATH] [--slug SLUG] <id>
  boulevard steward-key [--db PATH] [--slug SLUG]
  boulevard tokens         [--db PATH] [--slug SLUG]
  boulevard force-activate [--db PATH] [--slug SLUG] <handle>
  boulevard extend         [--db PATH] [--slug SLUG] <handle>
  boulevard revoke         [--db PATH] [--slug SLUG] <handle>
  boulevard export         [--db PATH] [--slug SLUG] --out PATH [--force]
  boulevard version
`)
}
