package main

import (
	"path/filepath"
	"testing"
)

func TestServeRejectsAMissingDatabase(t *testing.T) {
	// Starting a server against a database that does not exist would create
	// an empty one and serve 404s, which looks like a bug rather than a
	// missing setup step.
	code := runServe([]string{"--db", filepath.Join(t.TempDir(), "nope.db"), "--addr", "127.0.0.1:0"})
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestServeRejectsUnparsableFlags(t *testing.T) {
	if code := runServe([]string{"--nonsense"}); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}
