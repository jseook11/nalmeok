package course

import (
	"time"

	"github.com/jseook11/nalmeok/internal/config"
)

// SlotSlack is how far outside a class time a recording known only by one
// moment (its length unreadable) still counts as that class: a recorder
// started early or stopped a little late still lands here.
const SlotSlack = 20 * time.Minute

// A recording belongs to the class it shares the most time with, when that
// is at least MinOverlap and MinShare of the recording. Most students start
// recording a few minutes early; with back-to-back classes the start then
// falls in the class before, but almost all of the recording lies in the
// right one.
const (
	MinOverlap = 10 * time.Minute
	MinShare   = 0.25
)

// ByTime is the course a recording from start to end (local time) was made
// in. With a real span it is the class sharing the most time with the
// recording; a tie between two classes (a short recording across a break)
// is no answer, so the classifier decides. With start equal to end (length
// unknown) it is the class holding that moment, or the nearest within
// SlotSlack. ok is false when no class fits.
func ByTime(courses []config.Course, start, end time.Time) (config.Course, bool) {
	if start.IsZero() {
		return config.Course{}, false
	}
	if end.Sub(start) < time.Minute {
		return byMoment(courses, start)
	}
	start, end = start.Local(), end.Local()
	day := isoDay(start)
	from := minuteOfDay(start)
	// By the wall clock, so a DST change inside the recording counts right.
	// Past midnight the recording is cut there: the start's day decides.
	to := 24 * 60
	if y, m, d := end.Date(); y == start.Year() && m == start.Month() && d == start.Day() {
		to = minuteOfDay(end)
	}
	best, bestMin, tie := -1, 0, false
	for i, c := range courses {
		shared := 0
		for _, s := range c.Slots {
			a, b, ok := s.Minutes()
			if !ok || s.Day != day {
				continue
			}
			shared += max(0, min(to, b)-max(from, a))
		}
		switch {
		case shared > bestMin:
			best, bestMin, tie = i, shared, false
		case shared == bestMin && shared > 0:
			tie = true
		}
	}
	length := to - from
	if best < 0 || tie || bestMin < int(MinOverlap/time.Minute) || float64(bestMin) < MinShare*float64(length) {
		return config.Course{}, false
	}
	return courses[best], true
}

// byMoment: the class holding at, or the nearest within SlotSlack. Inside
// a class wins over near one; between two near classes the closer wins.
// Two classes equally near (13:00, between back-to-back classes; the
// moment may be the start or the stop) are no answer.
func byMoment(courses []config.Course, at time.Time) (config.Course, bool) {
	at = at.Local()
	day := isoDay(at)
	now := minuteOfDay(at)
	slack := int(SlotSlack / time.Minute)
	best, bestGap, tie := -1, slack+1, false
	for i, c := range courses {
		for _, s := range c.Slots {
			start, end, ok := s.Minutes()
			if !ok || s.Day != day {
				continue
			}
			gap := 0
			if now < start {
				gap = start - now
			} else if now > end {
				gap = now - end
			}
			switch {
			case gap < bestGap:
				best, bestGap, tie = i, gap, false
			case gap == bestGap && i != best:
				tie = true
			}
		}
	}
	if best < 0 || tie {
		return config.Course{}, false
	}
	return courses[best], true
}

func isoDay(t time.Time) int {
	if d := int(t.Weekday()); d != 0 {
		return d
	}
	return 7
}

func minuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }
