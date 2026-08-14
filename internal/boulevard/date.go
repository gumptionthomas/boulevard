// Package boulevard holds the domain types. It depends on nothing but the
// standard library.
package boulevard

import (
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// Date is a bare calendar date with no timezone. Period boundaries are
// calendar facts, not instants; see DESIGN.md §4 on why TOTP-style
// time derivation is rejected.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func NewDate(y int, m time.Month, d int) Date { return Date{Year: y, Month: m, Day: d} }

func DateFromTime(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return DateFromTime(t), nil
}

func (d Date) String() string { return d.time().Format(dateLayout) }

// time converts to a UTC instant at midnight. Used only for arithmetic and
// formatting — the zone is never observable outside this file.
func (d Date) time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) FirstOfMonth() Date { return NewDate(d.Year, d.Month, 1) }

// LastOfMonth relies on time.Date normalizing day 0 of the following month
// into the last day of this one, which handles leap years for free.
func (d Date) LastOfMonth() Date {
	return DateFromTime(time.Date(d.Year, d.Month+1, 0, 0, 0, 0, 0, time.UTC))
}

func (d Date) NextMonth() Date {
	return DateFromTime(time.Date(d.Year, d.Month+1, 1, 0, 0, 0, 0, time.UTC))
}

func (d Date) Before(o Date) bool { return d.time().Before(o.time()) }
func (d Date) After(o Date) bool  { return d.time().After(o.time()) }
func (d Date) Equal(o Date) bool  { return d == o }

func (d Date) MonthName() string { return d.Month.String() }

// NextDay returns the day after d. Every period ends on a month boundary, so
// in practice this is the first of the next month; the general form keeps
// the gapless-period test honest rather than tautological.
func (d Date) NextDay() Date {
	return DateFromTime(d.time().AddDate(0, 0, 1))
}
