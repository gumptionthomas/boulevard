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

func TestHandleWidthGrowsUntilItIsUnambiguous(t *testing.T) {
	// The queue printed four characters unconditionally, so a collision at
	// four left the steward reading "matches 2 items" with no way to type a
	// longer prefix for the one they wanted.
	for _, tc := range []struct {
		name string
		ids  []string
		want int
	}{
		{name: "distinct at four", ids: []string{"A7F3ZZQQ", "B100QQZZ"}, want: 4},
		{name: "collide at four", ids: []string{"A7F3ZZQQ", "A7F3YYQQ"}, want: 5},
		{name: "collide further in", ids: []string{"A7F3ZZQ1", "A7F3ZZQ2"}, want: 8},
		{name: "a single item never needs more", ids: []string{"A7F3ZZQQ"}, want: 4},
		{name: "nothing waiting", ids: nil, want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := handleWidth(items(tc.ids...)); got != tc.want {
				t.Errorf("handleWidth = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestQueueHandleIsAlwaysResolvable is the property that matters: whatever
// the queue prints, typing it back must name exactly one item.
func TestQueueHandleIsAlwaysResolvable(t *testing.T) {
	pending := items("A7F3ZZQQ", "A7F3ZZQ2", "B100QQZZ")
	w := handleWidth(pending)
	for _, want := range pending {
		got, err := resolvePrefix(pending, idPrefix(want.ID, w))
		if err != nil {
			t.Fatalf("the handle the queue printed for %s does not resolve: %v", want.ID, err)
		}
		if got.ID != want.ID {
			t.Errorf("handle for %s resolved to %s", want.ID, got.ID)
		}
	}
}

// TestResolvePrefixFoldsCrockfordLookalikes honors the reason the alphabet
// excludes I, L, O and U (DESIGN.md §4): a human cannot mistype one
// identifier into another. Uppercasing alone threw that away — someone who
// reads 0 as O was told nothing was waiting.
func TestResolvePrefixFoldsCrockfordLookalikes(t *testing.T) {
	pending := items("0123ZZ", "B100QQ")
	for _, typed := range []string{"0123", "O123", "o123", "0i23", "0L23", "0l23", "OI23"} {
		got, err := resolvePrefix(pending, typed)
		if err != nil {
			t.Errorf("resolvePrefix(%q): %v", typed, err)
			continue
		}
		if got.ID != "0123ZZ" {
			t.Errorf("resolvePrefix(%q) = %s, want 0123ZZ", typed, got.ID)
		}
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
