package tokens

import (
	"testing"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

func TestPlanRotationOnAFreshLibrary(t *testing.T) {
	today := boulevard.NewDate(2026, time.August, 22)
	got := PlanRotation(nil, today)
	if got.StartIndex != 1 {
		t.Errorf("StartIndex = %d, want 1", got.StartIndex)
	}
	if !got.Start.Equal(today) {
		t.Errorf("Start = %s, want %s", got.Start, today)
	}
}

// Mid-booklet: the active card survives and the next booklet begins the day
// after it ends. Indices continue from the next multiple of twelve so that
// booklet = (i-1)/12+1 and card = (i-1)%12+1 stay exact even though the
// discarded pending cards leave a gap.
func TestPlanRotationMidBooklet(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 1, ValidFrom: boulevard.NewDate(2026, time.August, 14), ValidUntil: boulevard.NewDate(2026, time.August, 31), State: boulevard.TokenExpired},
		{PeriodIndex: 2, ValidFrom: boulevard.NewDate(2026, time.September, 1), ValidUntil: boulevard.NewDate(2026, time.September, 30), State: boulevard.TokenActive},
		{PeriodIndex: 3, ValidFrom: boulevard.NewDate(2026, time.October, 1), ValidUntil: boulevard.NewDate(2026, time.October, 31), State: boulevard.TokenPending},
	}
	got := PlanRotation(toks, boulevard.NewDate(2026, time.September, 20))
	if got.StartIndex != 13 {
		t.Errorf("StartIndex = %d, want 13", got.StartIndex)
	}
	if want := boulevard.NewDate(2026, time.October, 1); !got.Start.Equal(want) {
		t.Errorf("Start = %s, want %s", got.Start, want)
	}
}

func TestPlanRotationTwiceKeepsTheArithmeticExact(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 24, ValidFrom: boulevard.NewDate(2027, time.August, 1), ValidUntil: boulevard.NewDate(2027, time.August, 31), State: boulevard.TokenActive},
	}
	got := PlanRotation(toks, boulevard.NewDate(2027, time.August, 15))
	if got.StartIndex != 25 {
		t.Errorf("StartIndex = %d, want 25", got.StartIndex)
	}
	if (got.StartIndex-1)/PeriodCount+1 != 3 {
		t.Errorf("booklet number = %d, want 3", (got.StartIndex-1)/PeriodCount+1)
	}
}

// Past month 12 there is nothing pending left; the new twelve simply follow
// the current card.
func TestPlanRotationPastTheLastCard(t *testing.T) {
	toks := []boulevard.Token{
		{PeriodIndex: 12, ValidFrom: boulevard.NewDate(2027, time.July, 1), ValidUntil: boulevard.NewDate(2027, time.July, 31), State: boulevard.TokenActive},
	}
	got := PlanRotation(toks, boulevard.NewDate(2027, time.July, 20))
	if got.StartIndex != 13 {
		t.Errorf("StartIndex = %d, want 13", got.StartIndex)
	}
	if want := boulevard.NewDate(2027, time.August, 1); !got.Start.Equal(want) {
		t.Errorf("Start = %s, want %s", got.Start, want)
	}
}
