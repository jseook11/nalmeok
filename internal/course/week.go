// Package course sorts finished transcripts into the user's lectures: it
// asks Gemini which lecture a transcript belongs to and what it is about,
// works out the semester week from when the audio was recorded, and names
// the file "<lecture>/<n>주차-<topic>.md".
package course

import (
	"time"
)

// SemesterStart is the first day of week 1 for a lecture recorded at t.
// override ("2026-09-01", from settings) wins when t falls in the half-year
// it starts. Otherwise the semester starts on the first weekday of March
// (from the 2nd; the 1st is a holiday) or of September, the usual Korean
// calendar. Recordings from January and February belong to the previous
// autumn semester's tail and get no week (zero time).
func SemesterStart(t time.Time, override string) time.Time {
	t = t.In(time.Local)
	if day, err := time.ParseInLocation(time.DateOnly, override, time.Local); err == nil {
		if !t.Before(day) && t.Before(day.AddDate(0, 6, 0)) {
			return day
		}
	}
	year := t.Year()
	switch {
	case t.Month() >= time.September:
		return firstWeekday(time.Date(year, time.September, 1, 0, 0, 0, 0, time.Local))
	case t.Month() >= time.March:
		return firstWeekday(time.Date(year, time.March, 2, 0, 0, 0, 0, time.Local))
	}
	return time.Time{}
}

// firstWeekday is day, or the Monday after it when it falls on a weekend.
func firstWeekday(day time.Time) time.Time {
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

// Week is the semester week of t: week 1 is the seven days from start (a
// Tuesday start runs Tuesday to Monday). Zero when there is no start or t
// is before it.
func Week(t, start time.Time) int {
	if start.IsZero() {
		return 0
	}
	t = t.In(time.Local)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	if day.Before(start) {
		return 0
	}
	// Calendar days, so a daylight-saving change cannot shift a week.
	days := int(day.Sub(start).Hours()/24 + 0.5)
	return days/7 + 1
}
