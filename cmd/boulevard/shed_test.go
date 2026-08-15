package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// shedOne puts one item on the shelf and then sheds it by expiry: leave,
// approve, then sweep with a clock well past the library's max_age_days
// (ninety days by default — see migrate.go's addLibrarySettings). This is
// the only shed reason reachable through exported store methods alone, which
// is what these CLI tests are allowed to use (store.Store does not export
// its *sql.DB, and this package cannot reach it directly).
func shedOne(t *testing.T, s *store.Store, lib boulevard.Library, id, note string) boulevard.Item {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC)

	leavePending(t, s, lib, id, note, base)
	if _, err := s.ApproveItem(ctx, lib.ID, id, base); err != nil {
		t.Fatalf("approve %s: %v", id, err)
	}
	if _, err := s.SweepExpiredItems(ctx, lib.ID, base.AddDate(0, 0, 200)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	it, err := s.ItemByID(ctx, lib.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if it.State != boulevard.ItemShed {
		t.Fatalf("shedOne(%s) left state %q, want shed", id, it.State)
	}
	return it
}

// fullShelfWithOneShedItem sheds one item, then fills the shelf to its
// default capacity of twelve with other items so the shed item cannot be
// reshelved without eviction.
func fullShelfWithOneShedItem(t *testing.T, s *store.Store, lib boulevard.Library) boulevard.Item {
	t.Helper()
	ctx := context.Background()
	shed := shedOne(t, s, lib, "C0DEZZZZZZZZZZZZZZZZZZZZZZ", "the one in the shed")

	base := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("F111%02dZZZZZZZZZZZZZZZZZZZZ", i)
		left := base.Add(time.Duration(i) * time.Minute)
		leavePending(t, s, lib, id, "filler", left)
		if _, err := s.ApproveItem(ctx, lib.ID, id, left); err != nil {
			t.Fatalf("approve filler %d: %v", i, err)
		}
	}
	return shed
}

func TestShedListsReasonsAndSanitizesNotes(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	// A note that would erase the line above it and draw a forged entry.
	shedOne(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "\x1b[1A\x1b[2Kforged entry")

	out := captureStdout(t, func() {
		if code := runShed([]string{"--db", path}); code != exitOK {
			t.Errorf("shed exit = %d, want %d", code, exitOK)
		}
	})

	if !strings.Contains(out, "expired") {
		t.Errorf("the listing does not name why the item shed: %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("an escape sequence reached the terminal — Sanitize is not applied")
	}
}

func TestReshelveOntoAFullShelfRefusesAndNamesWhy(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := fullShelfWithOneShedItem(t, s, lib)

	out := captureStdout(t, func() {
		if code := runReshelve([]string{"--db", path, it.ID[:4]}); code == exitOK {
			t.Error("reshelve onto a full shelf succeeded")
		}
	})
	if !strings.Contains(out, "full") {
		t.Errorf("the refusal does not say the shelf is full: %q", out)
	}
}

func TestReleaseFromTheShedSaysReleased(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := shedOne(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "a note")

	out := captureStdout(t, func() {
		if code := runRelease([]string{"--db", path, it.ID[:4]}); code != exitOK {
			t.Errorf("release exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "Released.") {
		t.Errorf("output = %q, want it to say Released.", out)
	}
	if got := stateOfItem(t, s, lib, it.ID); got != boulevard.ItemReleased {
		t.Errorf("state = %q, want released", got)
	}
}

func TestShedPrefixRefusesAnAmbiguousMatch(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	shedOne(t, s, lib, "AAAA1ZZZZZZZZZZZZZZZZZZZZZ", "first")
	shedOne(t, s, lib, "AAAA2ZZZZZZZZZZZZZZZZZZZZZ", "second")

	out := captureStdout(t, func() {
		if code := runReshelve([]string{"--db", path, "AAAA"}); code == exitOK {
			t.Error("an ambiguous prefix was resolved rather than refused")
		}
	})
	if !strings.Contains(out, "AAAA1") || !strings.Contains(out, "AAAA2") {
		t.Errorf("the refusal does not name both candidates: %q", out)
	}
}
