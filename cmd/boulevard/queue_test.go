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

// TestDisplayLineIsOneHarmlessLine covers the failure the queue's printing
// exists to prevent: a stranger's note is printed straight into the
// steward's terminal, so an escape sequence in it is a cursor instruction.
// A leaver who can move the cursor can draw a forged entry over a real one
// and get a different item approved than the one that was read.
func TestDisplayLineIsOneHarmlessLine(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text is untouched", "Reminded me of the alley cat.", "Reminded me of the alley cat."},
		{"a second line is dropped", "the real note\nthe forged one", "the real note …"},
		{"an escape is defused", "note\x1b[1A\x1b[2K", "note [1A [2K"},
		{"an escape before the break is defused too", "a\x1b[2Kb\nc", "a [2Kb …"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := displayLine(tc.in)
			if got != tc.want {
				t.Errorf("displayLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsAny(got, "\n\r\x1b") {
				t.Errorf("displayLine(%q) = %q, which can still steer a terminal", tc.in, got)
			}
		})
	}
}

func TestFirstLineCountsRunesNotBytes(t *testing.T) {
	// s[:70] splits a multi-byte rune and prints U+FFFD in place of what the
	// person wrote. The validation layer counts runes; so does this.
	long := strings.Repeat("é", 100)
	got := firstLine(long)
	if want := strings.Repeat("é", 70) + " …"; got != want {
		t.Errorf("firstLine truncated to %q, want %q", got, want)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("firstLine split a multi-byte rune")
	}
	if short := strings.Repeat("é", 70); firstLine(short) != short {
		t.Error("a payload exactly at the limit was truncated")
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
