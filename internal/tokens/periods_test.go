package tokens

import (
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func d(y int, m time.Month, day int) boulevard.Date { return boulevard.NewDate(y, m, day) }

func TestFirstPeriodRunsToEndOfInstallMonth(t *testing.T) {
	got := Periods(d(2026, time.August, 14), PeriodCount)
	if len(got) != PeriodCount {
		t.Fatalf("len = %d, want %d", len(got), PeriodCount)
	}
	first := got[0]
	if first.Index != 1 {
		t.Errorf("Index = %d, want 1", first.Index)
	}
	if !first.From.Equal(d(2026, time.August, 14)) {
		t.Errorf("From = %v, want 2026-08-14", first.From)
	}
	if !first.Until.Equal(d(2026, time.August, 31)) {
		t.Errorf("Until = %v, want 2026-08-31", first.Until)
	}
}

func TestLaterPeriodsAreWholeCalendarMonths(t *testing.T) {
	got := Periods(d(2026, time.August, 14), PeriodCount)
	second := got[1]
	if !second.From.Equal(d(2026, time.September, 1)) || !second.Until.Equal(d(2026, time.September, 30)) {
		t.Errorf("period 2 = %v..%v, want 2026-09-01..2026-09-30", second.From, second.Until)
	}
	last := got[PeriodCount-1]
	if !last.From.Equal(d(2027, time.July, 1)) || !last.Until.Equal(d(2027, time.July, 31)) {
		t.Errorf("period 12 = %v..%v, want 2027-07-01..2027-07-31", last.From, last.Until)
	}
}

func TestInstallOnLastDayGivesOneDayFirstPeriod(t *testing.T) {
	got := Periods(d(2026, time.September, 30), PeriodCount)
	if !got[0].From.Equal(d(2026, time.September, 30)) || !got[0].Until.Equal(d(2026, time.September, 30)) {
		t.Errorf("period 1 = %v..%v, want a single day 2026-09-30", got[0].From, got[0].Until)
	}
	if !got[1].From.Equal(d(2026, time.October, 1)) {
		t.Errorf("period 2 starts %v, want 2026-10-01", got[1].From)
	}
}

func TestInstallOnTheThirtyFirst(t *testing.T) {
	got := Periods(d(2026, time.January, 31), PeriodCount)
	if !got[0].Until.Equal(d(2026, time.January, 31)) {
		t.Errorf("period 1 ends %v, want 2026-01-31", got[0].Until)
	}
	// February must not be skipped by naive month addition.
	if !got[1].From.Equal(d(2026, time.February, 1)) || !got[1].Until.Equal(d(2026, time.February, 28)) {
		t.Errorf("period 2 = %v..%v, want all of February 2026", got[1].From, got[1].Until)
	}
}

func TestLeapYearFebruary(t *testing.T) {
	got := Periods(d(2028, time.January, 5), PeriodCount)
	if !got[1].Until.Equal(d(2028, time.February, 29)) {
		t.Errorf("period 2 ends %v, want 2028-02-29", got[1].Until)
	}
}

func TestPeriodsAreGaplessNonOverlappingAndOrdered(t *testing.T) {
	got := Periods(d(2026, time.November, 20), PeriodCount)
	for i, p := range got {
		if p.Index != i+1 {
			t.Errorf("period at slot %d has Index %d", i, p.Index)
		}
		if p.Until.Before(p.From) {
			t.Errorf("period %d ends before it starts: %v..%v", p.Index, p.From, p.Until)
		}
		if i > 0 {
			wantStart := got[i-1].Until.NextDay()
			if !p.From.Equal(wantStart) {
				t.Errorf("period %d starts %v, want %v — periods must be gapless", p.Index, p.From, wantStart)
			}
		}
	}
}

func TestPeriodsZeroOrNegativeReturnsEmpty(t *testing.T) {
	if got := Periods(d(2026, time.August, 14), 0); len(got) != 0 {
		t.Errorf("Periods(_, 0) returned %d periods, want 0", len(got))
	}
}
