package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// chicago is a fixed -05:00 zone standing in for the server's local time.
// A fixed zone rather than time.LoadLocation so the test does not depend on
// a tzdata database being installed.
var chicago = time.FixedZone("CDT", -5*60*60)

func TestHumanDeadline(t *testing.T) {
	now := time.Date(2026, time.September, 3, 16, 12, 0, 0, chicago)
	tests := []struct {
		name    string
		expires time.Time
		want    string
	}{
		{"same time tomorrow", now.Add(24 * time.Hour), "4:12 PM tomorrow"},
		{"later today", now.Add(3 * time.Hour), "7:12 PM today"},
		{"morning tomorrow", time.Date(2026, time.September, 4, 9, 5, 0, 0, chicago), "9:05 AM tomorrow"},
		{"further out falls back to a date", now.Add(72 * time.Hour), "4:12 PM on 6 September"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanDeadline(tc.expires, now); got != tc.want {
				t.Errorf("humanDeadline(%v, %v) = %q, want %q", tc.expires, now, got, tc.want)
			}
		})
	}
}

// TestHumanDeadlineRendersInTheClockZone pins the shape the store actually
// produces: expires comes back from SQLite in UTC (RFC3339 written as UTC,
// time.Parse returns UTC), while `now` is the server's local zone. Formatting
// the UTC value directly named a UTC clock time and, for an evening scan in
// a western zone, a UTC calendar day two days from today — which fell out of
// the "tomorrow" branch entirely. A test with UTC on both sides cannot see
// any of that.
func TestHumanDeadlineRendersInTheClockZone(t *testing.T) {
	tests := []struct {
		name    string
		now     time.Time
		expires time.Time
		want    string
	}{
		{
			// 14 Aug 2026 20:25 CDT + 24h. In UTC the deadline is
			// 16 Aug 01:25, two calendar days after 14 Aug local.
			name:    "evening scan stays tomorrow",
			now:     time.Date(2026, time.August, 14, 20, 25, 17, 0, chicago),
			expires: time.Date(2026, time.August, 16, 1, 25, 17, 0, time.UTC),
			want:    "8:25 PM tomorrow",
		},
		{
			name:    "morning scan stays today",
			now:     time.Date(2026, time.August, 14, 6, 0, 0, 0, chicago),
			expires: time.Date(2026, time.August, 14, 20, 0, 0, 0, time.UTC),
			want:    "3:00 PM today",
		},
		{
			// The old sameYear guard dropped a genuine "tomorrow" into
			// the date fallback across the new year.
			name:    "new year's eve is still tomorrow",
			now:     time.Date(2026, time.December, 31, 18, 0, 0, 0, chicago),
			expires: time.Date(2027, time.January, 1, 23, 0, 0, 0, time.UTC),
			want:    "6:00 PM tomorrow",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanDeadline(tc.expires, tc.now); got != tc.want {
				t.Errorf("humanDeadline(%v, %v) = %q, want %q", tc.expires, tc.now, got, tc.want)
			}
		})
	}
}

func TestHumanDate(t *testing.T) {
	today := boulevard.NewDate(2026, time.August, 14)
	tests := []struct {
		name string
		d    boulevard.Date
		want string
	}{
		{"this year omits it", boulevard.NewDate(2026, time.September, 7), "7 September"},
		{"another year names it", boulevard.NewDate(2027, time.June, 24), "24 June 2027"},
		{"a past year names it too", boulevard.NewDate(2025, time.December, 3), "3 December 2025"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanDate(tc.d, today); got != tc.want {
				t.Errorf("humanDate(%v, %v) = %q, want %q", tc.d, today, got, tc.want)
			}
		})
	}
}

func TestTemplatesAllParse(t *testing.T) {
	// New panics if a template fails to parse; this catches a broken
	// template at test time rather than on a steward's first request.
	srv := New(nil, time.Now)
	for _, name := range []string{"shelf.html", "about.html", "outdated.html", "invalid.html"} {
		set, ok := srv.tmpl[name]
		if !ok || set.Lookup(name) == nil {
			t.Errorf("template %q did not parse", name)
		}
	}
}

// TestRenderedPagesAreDistinct renders each page for real and checks its
// output, rather than just confirming a name is present in the template
// set. A shared `{{define "body"}}` block across page files previously let
// the last one parsed silently win, so every page rendered shelf.html's
// body — Lookup alone can't catch that, since all four names still resolve
// to something. invalid.html is the case that matters most: it must never
// leak a library name, location or shelf link, because an unknown token
// secret resolves to no library at all, and doing so for a revoked token
// would confirm the secret was real to whoever holds the card.
func TestRenderedPagesAreDistinct(t *testing.T) {
	srv := New(nil, time.Now)

	render := func(name string, data any) string {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.render(rec, http.StatusOK, name, data)
		if rec.Code != http.StatusOK {
			t.Fatalf("render %s: status = %d, want 200", name, rec.Code)
		}
		return rec.Body.String()
	}

	t.Run("invalid.html renders no library identity", func(t *testing.T) {
		body := render("invalid.html", map[string]any{"Title": "That code isn't valid."})
		for _, want := range []string{"That code isn't valid.", "scan the code inside the box door"} {
			if !strings.Contains(body, want) {
				t.Errorf("invalid.html missing %q\n%s", want, body)
			}
		}
		for _, forbidden := range []string{"Riverside Little Free Library", "413 Elm", "/b/"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("invalid.html leaked %q\n%s", forbidden, body)
			}
		}
	})

	t.Run("outdated.html renders the card-expired copy and the library name", func(t *testing.T) {
		body := render("outdated.html", map[string]any{
			"Title":       "Card out of date",
			"LibraryName": "Riverside Little Free Library",
			"CardLabel":   "Card 3",
			"StoppedOn":   "3 September",
			"ShelfURL":    "/b/riverside/",
		})
		for _, want := range []string{"This card is out of date.", "Riverside Little Free Library"} {
			if !strings.Contains(body, want) {
				t.Errorf("outdated.html missing %q\n%s", want, body)
			}
		}
	})

	t.Run("shelf.html renders the shelf's empty state", func(t *testing.T) {
		body := render("shelf.html", map[string]any{
			"Title":       "Riverside Little Free Library",
			"LibraryName": "Riverside Little Free Library",
			"Deadline":    "",
		})
		if !strings.Contains(body, "The shelf is empty.") {
			t.Errorf("shelf.html missing empty-shelf text\n%s", body)
		}
	})

	t.Run("about.html renders the about copy, not the shelf's", func(t *testing.T) {
		body := render("about.html", map[string]any{
			"Title":       "About Riverside Little Free Library",
			"LibraryName": "Riverside Little Free Library",
			"Location":    "413 Elm St",
			"SourceURL":   "https://example.com/source",
			"BuildLine":   "v0.1.0",
		})
		if !strings.Contains(body, "About this shelf") {
			t.Errorf("about.html missing about copy\n%s", body)
		}
		if strings.Contains(body, "The shelf is empty.") {
			t.Errorf("about.html rendered shelf.html's body instead of its own\n%s", body)
		}
	})
}
