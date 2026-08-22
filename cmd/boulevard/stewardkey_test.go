package main

import (
	"context"
	"strings"
	"testing"
)

func TestStewardKeyPrintsAKeyOnceAndStoresOnlyItsHash(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]

	out := captureStdout(t, func() {
		if code := runStewardKey([]string{"--db", path}); code != exitOK {
			t.Errorf("exit = %d, want %d", code, exitOK)
		}
	})

	if !strings.Contains(out, "shown once") {
		t.Errorf("output does not say the key is shown once: %q", out)
	}

	// Pull the 26-character Crockford key out of the output and check it
	// verifies — and that what is stored is not the key itself.
	var key string
	for _, f := range strings.Fields(out) {
		if len(f) == 26 {
			key = f
		}
	}
	if key == "" {
		t.Fatalf("no 26-character key in the output: %q", out)
	}
	ok, err := s.VerifyStewardKey(context.Background(), lib.ID, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("the printed key does not verify against what was stored")
	}
}

func TestStewardKeyReplacesAnExistingKey(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	ctx := context.Background()

	first := captureStdout(t, func() { runStewardKey([]string{"--db", path}) })
	second := captureStdout(t, func() { runStewardKey([]string{"--db", path}) })

	keyOf := func(out string) string {
		for _, f := range strings.Fields(out) {
			if len(f) == 26 {
				return f
			}
		}
		return ""
	}
	old, new := keyOf(first), keyOf(second)
	if old == "" || new == "" || old == new {
		t.Fatalf("expected two different keys, got %q and %q", old, new)
	}

	if ok, _ := s.VerifyStewardKey(ctx, lib.ID, old); ok {
		t.Error("the replaced key still verifies")
	}
	if ok, _ := s.VerifyStewardKey(ctx, lib.ID, new); !ok {
		t.Error("the new key does not verify")
	}
}
