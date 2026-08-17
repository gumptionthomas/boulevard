package main

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// The tests below drive the commands themselves rather than their pieces.
// This is the milestone's only steward interface, and the acceptance run
// that would otherwise exercise it needs a printed booklet and a phone —
// so the wiring from PendingItems through resolvePrefix to ApproveItem, the
// exit codes, and the two sentences the steward reads are worth pinning
// here instead.

// cliStore creates a database with one library and returns both.
func cliStore(t *testing.T, slugs ...string) (string, *store.Store, []boulevard.Library) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	var libs []boulevard.Library
	for _, slug := range slugs {
		id, err := boulevard.NewLibraryID(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		lib := boulevard.Library{
			ID: id, Slug: slug, Name: "The " + slug + " Boulevard",
			LocationLabel: "4th & Fairview", BaseURL: "https://example.org",
		}
		if err := s.CreateLibrary(context.Background(), lib); err != nil {
			t.Fatal(err)
		}
		libs = append(libs, lib)
	}
	return path, s, libs
}

// leavePending puts an item in the queue, exactly as the leave form does.
//
// The id is given rather than generated, because half of what these tests
// are about is which characters the steward types. Two random ids are
// unambiguous at four characters all but once in a million runs, which is
// no way to test the ambiguous case.
func leavePending(t *testing.T, s *store.Store, lib boulevard.Library, id, note string, left time.Time) boulevard.Item {
	t.Helper()
	it := boulevard.Item{
		ID: id, LibraryID: lib.ID, Type: boulevard.ItemLink,
		Payload: "https://example.org/" + id, Note: note,
		State: boulevard.ItemPending, LeftAt: left,
	}
	if err := s.CreateItem(context.Background(), lib.ID, it); err != nil {
		t.Fatal(err)
	}
	return it
}

func stateOfItem(t *testing.T, s *store.Store, lib boulevard.Library, itemID string) boulevard.ItemState {
	t.Helper()
	got, err := s.ItemByID(context.Background(), lib.ID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	return got.State
}

// What the steward reads is part of these commands' contract, so the
// assertions below go through captureStdout (booklet_test.go) and read the
// same stream a steward does.

func TestApproveShelvesTheItemItNamed(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := leavePending(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "Reminded me of the alley cat.", time.Now().UTC())

	out := captureStdout(t, func() {
		if code := runApprove([]string{"--db", path, it.ID[:4]}); code != exitOK {
			t.Errorf("approve exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "Shelved.") {
		t.Errorf("approve printed %q, want it to say Shelved.", out)
	}
	if strings.Contains(out, "moved to the shed") {
		t.Errorf("approve claimed an eviction on an empty shelf: %q", out)
	}
	if got := stateOfItem(t, s, lib, it.ID); got != boulevard.ItemShelved {
		t.Errorf("state = %q, want shelved", got)
	}
}

func TestRejectReleasesTheItemItNamed(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	it := leavePending(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "no thanks", time.Now().UTC())

	out := captureStdout(t, func() {
		if code := runReject([]string{"--db", path, it.ID[:4]}); code != exitOK {
			t.Errorf("reject exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "Released.") {
		t.Errorf("reject printed %q, want it to say Released.", out)
	}
	if got := stateOfItem(t, s, lib, it.ID); got != boulevard.ItemReleased {
		t.Errorf("state = %q, want released — §5 calls release a soft delete", got)
	}
}

// TestApproveIntoAFullShelfSaysWhatItShed is the one message the steward
// cannot infer from anywhere else in this milestone: there is no shed view
// until Milestone 3, so an item leaving the shelf silently would just look
// like it vanished.
func TestApproveIntoAFullShelfSaysWhatItShed(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	// Narrow the shelf the way docs/shelf-acceptance.md tells a steward to,
	// since there is no settings command until Milestone 4.
	stored, err := s.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.Slots = 1
	if err := s.UpdateLibrary(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	first := leavePending(t, s, lib, "AAAAZZZZZZZZZZZZZZZZZZZZZZ", "the first", base)
	if code := runApprove([]string{"--db", path, first.ID[:4]}); code != exitOK {
		t.Fatalf("approving onto an empty shelf failed with %d", code)
	}
	second := leavePending(t, s, lib, "BBBBZZZZZZZZZZZZZZZZZZZZZZ", "the second", base.Add(time.Hour))

	out := captureStdout(t, func() {
		if code := runApprove([]string{"--db", path, second.ID[:4]}); code != exitOK {
			t.Errorf("approve exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "the oldest item moved to the shed") {
		t.Errorf("approve printed %q, want it to name the eviction", out)
	}
	if got := stateOfItem(t, s, lib, first.ID); got != boulevard.ItemShed {
		t.Errorf("the evicted item is %q, want shed", got)
	}
	if got := stateOfItem(t, s, lib, second.ID); got != boulevard.ItemShelved {
		t.Errorf("the newcomer is %q, want shelved", got)
	}
}

// TestApproveOntoAnAllPinnedShelfNamesThePins is the CLI half of the
// refusal ApproveItem's own comment described as unreachable until this
// milestone: a full shelf where every item is pinned. Modeled on
// TestReshelveOntoAFullShelfRefusesAndNamesWhy in shed_test.go. Spec §10
// requires the refusal name the pins, not just count them, so this checks
// the pinned item's handle appears in the refusal rather than only the word
// "pinned".
func TestApproveOntoAnAllPinnedShelfNamesThePins(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)

	pinned := leavePending(t, s, lib, "AAAAZZZZZZZZZZZZZZZZZZZZZZ", "the pin", base)
	if code := runApprove([]string{"--db", path, pinned.ID[:4]}); code != exitOK {
		t.Fatalf("approving the item to pin failed with %d", code)
	}
	if err := s.PinItem(context.Background(), lib.ID, pinned.ID, 3); err != nil {
		t.Fatalf("pin: %v", err)
	}

	stored, err := s.LibraryBySlug(context.Background(), lib.Slug)
	if err != nil {
		t.Fatal(err)
	}
	stored.Slots = 1
	if err := s.UpdateLibrary(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	waiting := leavePending(t, s, lib, "BBBBZZZZZZZZZZZZZZZZZZZZZZ", "waiting", base.Add(time.Hour))

	out := captureStderr(t, func() {
		if code := runApprove([]string{"--db", path, waiting.ID[:4]}); code == exitOK {
			t.Error("approve onto an all-pinned shelf succeeded")
		}
	})
	if !strings.Contains(out, "nothing was approved") {
		t.Errorf("the refusal does not say nothing was approved: %q", out)
	}
	if !strings.Contains(out, strings.ToLower(pinned.ID[:4])) {
		t.Errorf("the refusal does not name the pinned item blocking it: %q", out)
	}
	if got := stateOfItem(t, s, lib, waiting.ID); got != boulevard.ItemPending {
		t.Errorf("the refused item is %q, want it still pending", got)
	}
	if got := stateOfItem(t, s, lib, pinned.ID); got != boulevard.ItemShelved {
		t.Errorf("the pinned item is %q, want it still shelved and unevicted", got)
	}
}

// An ambiguous prefix is the steward's mistake, not the database's, so it
// must not read as an I/O failure to whatever is reading the exit code.
func TestDecideRefusesAnAmbiguousPrefix(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	a := leavePending(t, s, lib, "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "one", base)
	b := leavePending(t, s, lib, "A7F3YYYYYYYYYYYYYYYYYYYYYY", "two", base.Add(time.Minute))

	const shared = "a7f3" // the four characters `queue` would have printed
	for _, verb := range []struct {
		name string
		run  func([]string) int
	}{{"approve", runApprove}, {"reject", runReject}} {
		t.Run(verb.name, func(t *testing.T) {
			if code := verb.run([]string{"--db", path, shared}); code != exitUsage {
				t.Errorf("exit = %d, want %d — refusing to guess is a usage error", code, exitUsage)
			}
		})
	}
	// And nothing was decided.
	if got := stateOfItem(t, s, lib, a.ID); got != boulevard.ItemPending {
		t.Errorf("item a is %q, want it untouched", got)
	}
	if got := stateOfItem(t, s, lib, b.ID); got != boulevard.ItemPending {
		t.Errorf("item b is %q, want it untouched", got)
	}
}

func TestDecideRefusesAPrefixMatchingNothing(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	leavePending(t, s, libs[0], "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "waiting", time.Now().UTC())
	if code := runApprove([]string{"--db", path, "ZZZZ"}); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
}

// A database holding more than one library must refuse to pick, the same
// way GET / does. Reachable today: `boulevard booklet` run twice with
// different names puts two libraries in one file.
func TestQueueCommandsRefuseToGuessBetweenLibraries(t *testing.T) {
	path, s, libs := cliStore(t, "fairview", "whittier")
	it := leavePending(t, s, libs[0], "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "waiting", time.Now().UTC())

	for _, tc := range []struct {
		name string
		args []string
		run  func([]string) int
	}{
		{"queue", []string{"--db", path}, runQueue},
		{"approve", []string{"--db", path, it.ID[:4]}, runApprove},
		{"reject", []string{"--db", path, it.ID[:4]}, runReject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := tc.run(tc.args); code != exitUsage {
				t.Errorf("exit = %d, want %d without --slug", code, exitUsage)
			}
		})
	}
	if got := stateOfItem(t, s, libs[0], it.ID); got != boulevard.ItemPending {
		t.Errorf("state = %q; a refusal must decide nothing", got)
	}

	// With --slug, the same command works.
	if code := runApprove([]string{"--db", path, "--slug", "fairview", it.ID[:4]}); code != exitOK {
		t.Errorf("approve --slug exit = %d, want %d", code, exitOK)
	}
	if got := stateOfItem(t, s, libs[0], it.ID); got != boulevard.ItemShelved {
		t.Errorf("state = %q, want shelved", got)
	}
}

// A prefix resolved against the named library only: an item waiting at one
// box must not be approvable through another's --slug.
func TestDecideResolvesWithinOneLibraryOnly(t *testing.T) {
	path, s, libs := cliStore(t, "fairview", "whittier")
	it := leavePending(t, s, libs[0], "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "waiting at fairview", time.Now().UTC())

	if code := runApprove([]string{"--db", path, "--slug", "whittier", it.ID[:4]}); code != exitUsage {
		t.Errorf("exit = %d, want %d — the prefix names nothing at whittier", code, exitUsage)
	}
	if got := stateOfItem(t, s, libs[0], it.ID); got != boulevard.ItemPending {
		t.Errorf("state = %q; an item must not be decidable through another library", got)
	}
}

// Approving the same item twice is the steward repeating themselves, not
// the database failing.
func TestApproveTwiceIsAUsageError(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	it := leavePending(t, s, libs[0], "A7F3ZZZZZZZZZZZZZZZZZZZZZZ", "twice", time.Now().UTC())
	if code := runApprove([]string{"--db", path, it.ID[:4]}); code != exitOK {
		t.Fatalf("first approve exit = %d", code)
	}
	if code := runApprove([]string{"--db", path, it.ID[:4]}); code != exitUsage {
		t.Errorf("second approve exit = %d, want %d", code, exitUsage)
	}
}

func TestQueueListsWhatIsWaitingOldestFirst(t *testing.T) {
	path, s, libs := cliStore(t, "fairview")
	lib := libs[0]
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	first := leavePending(t, s, lib, "AAAAZZZZZZZZZZZZZZZZZZZZZZ", "left first", base)
	leavePending(t, s, lib, "BBBBZZZZZZZZZZZZZZZZZZZZZZ", "left second", base.Add(time.Hour))

	out := captureStdout(t, func() {
		if code := runQueue([]string{"--db", path}); code != exitOK {
			t.Errorf("queue exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "2 waiting for") {
		t.Errorf("queue printed %q, want a count", out)
	}
	if i, j := strings.Index(out, "left first"), strings.Index(out, "left second"); i < 0 || j < 0 || i > j {
		t.Errorf("queue is not oldest-first:\n%s", out)
	}
	// The handle it prints must be the handle it accepts.
	handle := strings.ToLower(first.ID[:4])
	if !strings.Contains(out, "["+handle+"]") {
		t.Errorf("queue did not print the handle %q:\n%s", handle, out)
	}
	if code := runApprove([]string{"--db", path, handle}); code != exitOK {
		t.Errorf("the handle the queue printed did not resolve: exit %d", code)
	}
}

func TestQueueOnAnEmptyQueueSaysSo(t *testing.T) {
	path, _, _ := cliStore(t, "fairview")
	out := captureStdout(t, func() {
		if code := runQueue([]string{"--db", path}); code != exitOK {
			t.Errorf("queue exit = %d, want %d", code, exitOK)
		}
	})
	if !strings.Contains(out, "Nothing waiting") {
		t.Errorf("queue printed %q", out)
	}
}

// A database with no libraries cannot be answered with --slug, so it must
// not be told to use one.
func TestQueueOnADatabaseWithNoLibrariesPointsAtBooklet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	if code := runQueue([]string{"--db", path}); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
}
