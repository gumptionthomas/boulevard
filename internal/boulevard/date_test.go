package boulevard

import (
	"testing"
	"time"
)

func TestLastOfMonth(t *testing.T) {
	tests := []struct {
		name string
		in   Date
		want Date
	}{
		{"31-day month", NewDate(2026, time.August, 14), NewDate(2026, time.August, 31)},
		{"30-day month", NewDate(2026, time.September, 1), NewDate(2026, time.September, 30)},
		{"february common year", NewDate(2027, time.February, 3), NewDate(2027, time.February, 28)},
		{"february leap year", NewDate(2028, time.February, 3), NewDate(2028, time.February, 29)},
		{"already last day", NewDate(2026, time.December, 31), NewDate(2026, time.December, 31)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.LastOfMonth(); !got.Equal(tc.want) {
				t.Errorf("LastOfMonth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextMonthReturnsFirstDay(t *testing.T) {
	tests := []struct {
		name string
		in   Date
		want Date
	}{
		{"mid month", NewDate(2026, time.August, 14), NewDate(2026, time.September, 1)},
		{"from the 31st", NewDate(2026, time.January, 31), NewDate(2026, time.February, 1)},
		{"year rollover", NewDate(2026, time.December, 15), NewDate(2027, time.January, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.NextMonth(); !got.Equal(tc.want) {
				t.Errorf("NextMonth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringAndParseRoundTrip(t *testing.T) {
	d := NewDate(2026, time.August, 14)
	if got := d.String(); got != "2026-08-14" {
		t.Fatalf("String() = %q, want %q", got, "2026-08-14")
	}
	back, err := ParseDate("2026-08-14")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if !back.Equal(d) {
		t.Errorf("round trip = %v, want %v", back, d)
	}
}

func TestParseDateRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "2026-13-01", "not-a-date", "2026/08/14"} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q) succeeded, want error", s)
		}
	}
}

func TestOrdering(t *testing.T) {
	early, late := NewDate(2026, time.August, 14), NewDate(2026, time.September, 1)
	if !early.Before(late) {
		t.Error("early.Before(late) = false")
	}
	if !late.After(early) {
		t.Error("late.After(early) = false")
	}
	if early.Before(early) || early.After(early) {
		t.Error("a date must be neither before nor after itself")
	}
}

func TestMonthName(t *testing.T) {
	if got := NewDate(2026, time.August, 14).MonthName(); got != "August" {
		t.Errorf("MonthName() = %q, want %q", got, "August")
	}
}
