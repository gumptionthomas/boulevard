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
  boulevard version
`)
}
