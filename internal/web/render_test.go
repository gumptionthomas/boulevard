package web

import (
	"testing"
	"time"
)

func TestHumanDeadline(t *testing.T) {
	now := time.Date(2026, time.September, 3, 16, 12, 0, 0, time.UTC)
	tests := []struct {
		name    string
		expires time.Time
		want    string
	}{
		{"same time tomorrow", now.Add(24 * time.Hour), "4:12 PM tomorrow"},
		{"later today", now.Add(3 * time.Hour), "7:12 PM today"},
		{"morning tomorrow", time.Date(2026, time.September, 4, 9, 5, 0, 0, time.UTC), "9:05 AM tomorrow"},
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

func TestTemplatesAllParse(t *testing.T) {
	// New panics if a template fails to parse; this catches a broken
	// template at test time rather than on a steward's first request.
	srv := New(nil, time.Now)
	for _, name := range []string{"shelf.html", "about.html", "outdated.html", "invalid.html"} {
		if srv.tmpl.Lookup(name) == nil {
			t.Errorf("template %q did not parse", name)
		}
	}
}
