package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func items(ids ...string) []boulevard.Item {
	out := make([]boulevard.Item, 0, len(ids))
	for _, id := range ids {
		out = append(out, boulevard.Item{ID: id, Note: "note for " + id, LeftAt: time.Now()})
	}
	return out
}

func TestResolvePrefixFindsAUniqueMatch(t *testing.T) {
	got, err := resolvePrefix(items("A7F3ZZ", "B100QQ"), "a7f3")
	if err != nil {
		t.Fatalf("resolvePrefix: %v", err)
	}
	if got.ID != "A7F3ZZ" {
		t.Errorf("ID = %q, want A7F3ZZ", got.ID)
	}
}

func TestResolvePrefixIsCaseInsensitive(t *testing.T) {
	if _, err := resolvePrefix(items("A7F3ZZ"), "A7F3"); err != nil {
		t.Errorf("uppercase prefix: %v", err)
	}
	if _, err := resolvePrefix(items("A7F3ZZ"), "a7f3"); err != nil {
		t.Errorf("lowercase prefix: %v", err)
	}
}

func TestResolvePrefixRefusesAnAmbiguousPrefix(t *testing.T) {
	// Approving the wrong thing because a prefix silently resolved to it is
	// the failure this design is guarding against.
	_, err := resolvePrefix(items("A7F3ZZ", "A7F3YY"), "a7f3")
	if err == nil {
		t.Fatal("ambiguous prefix resolved, want an error")
	}
	for _, want := range []string{"A7F3ZZ", "A7F3YY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name candidate %q: %v", want, err)
		}
	}
}

func TestResolvePrefixRefusesNoMatch(t *testing.T) {
	if _, err := resolvePrefix(items("A7F3ZZ"), "zzzz"); err == nil {
		t.Error("a prefix matching nothing resolved, want an error")
	}
}

func TestResolvePrefixRefusesAnEmptyPrefix(t *testing.T) {
	if _, err := resolvePrefix(items("A7F3ZZ", "B100QQ"), ""); err == nil {
		t.Error("an empty prefix resolved, want an error")
	}
}

func TestQueueCommandsRejectAMissingDatabase(t *testing.T) {
	for name, run := range map[string]func([]string) int{
		"queue":   runQueue,
		"approve": runApprove,
		"reject":  runReject,
	} {
		args := []string{"--db", t.TempDir() + "/nope.db"}
		if name != "queue" {
			args = append(args, "abcd")
		}
		if code := run(args); code != exitUsage {
			t.Errorf("%s exit = %d, want %d", name, code, exitUsage)
		}
	}
}
