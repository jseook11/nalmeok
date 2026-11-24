package course

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSemesterStartDefaults(t *testing.T) {
	cases := map[string]string{
		"2026-09-01 10:00": "2026-09-01", // Tuesday
		"2026-11-24 13:00": "2026-09-01",
		"2026-03-10 09:00": "2026-03-02", // Monday
		"2027-03-05 09:00": "2027-03-02", // Tuesday
		"2025-09-03 09:00": "2025-09-01", // Monday
		"2024-09-10 09:00": "2024-09-02", // Sep 1 2024 is a Sunday
	}
	for rec, want := range cases {
		if got := SemesterStart(day(rec), "").Format(time.DateOnly); got != want {
			t.Errorf("SemesterStart(%s) = %s, want %s", rec, got, want)
		}
	}
	if got := SemesterStart(day("2026-01-15 10:00"), ""); !got.IsZero() {
		t.Errorf("January start = %v, want none", got)
	}
}

func TestSemesterStartOverride(t *testing.T) {
	// An early start counts for recordings in its half-year only.
	if got := SemesterStart(day("2026-08-27 10:00"), "2026-08-24").Format(time.DateOnly); got != "2026-08-24" {
		t.Errorf("override = %s", got)
	}
	if got := SemesterStart(day("2027-03-10 10:00"), "2026-08-24").Format(time.DateOnly); got != "2027-03-02" {
		t.Errorf("next semester = %s, want the default", got)
	}
	if got := SemesterStart(day("2026-11-24 10:00"), "garbage").Format(time.DateOnly); got != "2026-09-01" {
		t.Errorf("bad override = %s", got)
	}
}

func TestWeekRunsFromTheStartDay(t *testing.T) {
	start := day("2026-09-01 00:00") // Tuesday
	cases := map[string]int{
		"2026-08-31 10:00": 0,
		"2026-09-01 09:00": 1,
		"2026-09-07 23:59": 1, // the next Monday is still week 1
		"2026-09-08 09:00": 2,
		"2026-11-24 13:00": 13,
	}
	for rec, want := range cases {
		if got := Week(day(rec), start); got != want {
			t.Errorf("Week(%s) = %d, want %d", rec, got, want)
		}
	}
	if got := Week(day("2026-09-01 09:00"), time.Time{}); got != 0 {
		t.Errorf("no start = %d", got)
	}
}
