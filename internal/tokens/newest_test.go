package tokens

import (
	"testing"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// tokensAt builds throwaway tokens at the given period indices, in whatever
// order the caller passes — NewestBooklet must not depend on input order,
// since TokensForLibrary's own ordering is not this package's concern.
func tokensAt(indices ...int) []boulevard.Token {
	out := make([]boulevard.Token, 0, len(indices))
	for _, i := range indices {
		out = append(out, boulevard.Token{PeriodIndex: i})
	}
	return out
}

func indicesOf(toks []boulevard.Token) []int {
	out := make([]int, len(toks))
	for i, tok := range toks {
		out[i] = tok.PeriodIndex
	}
	return out
}

func TestNewestBookletOnAnUnrotatedLibrary(t *testing.T) {
	toks := tokensAt(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
	got := NewestBooklet(toks)
	if len(got) != 12 {
		t.Fatalf("len = %d, want 12: %v", len(got), indicesOf(got))
	}
	for _, tok := range got {
		if tok.PeriodIndex < 1 || tok.PeriodIndex > 12 {
			t.Errorf("unrotated library returned an out-of-range index: %v", indicesOf(got))
		}
	}
}

func TestNewestBookletAfterOneRotation(t *testing.T) {
	// The active card (period 3) survives a rotation and sits alongside
	// booklet 2's fresh twelve (13-24) — the exact shape RotatePendingTokens
	// leaves behind.
	toks := tokensAt(3, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24)
	got := NewestBooklet(toks)
	want := []int{13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), indicesOf(got))
	}
	seen := make(map[int]bool, len(got))
	for _, tok := range got {
		seen[tok.PeriodIndex] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("missing period %d from the newest booklet: %v", w, indicesOf(got))
		}
	}
	if seen[3] {
		t.Errorf("booklet 1's surviving active card leaked into the newest booklet: %v", indicesOf(got))
	}
}

func TestNewestBookletAfterTwoRotations(t *testing.T) {
	// Booklet 1's active card (14, mid-way through booklet 2 when it
	// force-activated) plus booklet 3's fresh 25-36 — booklet 2's own
	// pending remainder was discarded by the second rotation, the same way
	// RotatePendingTokens discards booklet 1's remainder on the first.
	toks := tokensAt(14, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36)
	got := NewestBooklet(toks)
	if len(got) != 12 {
		t.Fatalf("len = %d, want 12: %v", len(got), indicesOf(got))
	}
	for _, tok := range got {
		if tok.PeriodIndex < 25 || tok.PeriodIndex > 36 {
			t.Errorf("selected a token outside booklet 3: %v", indicesOf(got))
		}
	}
}

func TestNewestBookletWithFewerThanTwelve(t *testing.T) {
	// A rotation caught mid-transaction, or a library nobody has minted a
	// booklet for yet. NewestBooklet selects what is there; it is the
	// caller's job to notice the count is short and refuse.
	toks := tokensAt(13, 14, 15)
	got := NewestBooklet(toks)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %v", len(got), indicesOf(got))
	}
}

func TestNewestBookletOnNoTokens(t *testing.T) {
	got := NewestBooklet(nil)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0: %v", len(got), indicesOf(got))
	}
}
