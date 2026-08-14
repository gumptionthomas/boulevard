package main

import (
	"fmt"

	"github.com/gumptionthomas/boulevard/internal/version"
)

func runVersion() int {
	fmt.Println(version.String())
	return exitOK
}
