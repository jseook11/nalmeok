package course

import (
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
)

// One moment (length unknown): the class holding it, or the nearest within
// SlotSlack.
func TestByTimeMoment(t *testing.T) {
	courses := []config.Course{
		{Name: "선형대수학", Slots: []config.Slot{{Day: 2, Start: "10:30", End: "11:45"}, {Day: 4, Start: "10:30", End: "11:45"}}},
		{Name: "알고리즘", Slots: []config.Slot{{Day: 2, Start: "12:00", End: "13:15"}}},
		{Name: "자습"},
	}
	// 2026-11-10 is a Tuesday.
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.Local) }
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"inside", at(10, 11, 0), "선형대수학"},
		{"same slot another day", at(12, 10, 31), "선형대수학"},
		{"started early", at(10, 10, 12), "선형대수학"},
		{"stopped late (modification time)", at(10, 13, 30), "알고리즘"},
		{"between back-to-back classes, closer wins", at(10, 11, 56), "알고리즘"},
		{"inside beats near", at(10, 12, 0), "알고리즘"},
		{"too early", at(10, 10, 5), ""},
		{"other day", at(11, 11, 0), ""},
		{"zero time", time.Time{}, ""},
	}
	for _, tt := range tests {
		got, ok := ByTime(courses, tt.at, tt.at)
		if got.Name != tt.want || ok != (tt.want != "") {
			t.Errorf("%s: ByTime = %q, %v; want %q", tt.name, got.Name, ok, tt.want)
		}
	}
}

// A span: the class sharing the most time, so a recording started a few
// minutes early in the class before goes to the right one.
func TestByTimeSpan(t *testing.T) {
	courses := []config.Course{
		{Name: "논리회로", Slots: []config.Slot{{Day: 2, Start: "11:00", End: "13:00"}}},
		{Name: "컴퓨터구조", Slots: []config.Slot{{Day: 2, Start: "13:00", End: "15:00"}}},
		{Name: "선형대수학", Slots: []config.Slot{{Day: 2, Start: "16:00", End: "17:15"}}},
		{Name: "야간세미나", Slots: []config.Slot{{Day: 2, Start: "22:00", End: "23:50"}}},
	}
	// 2026-11-10 is a Tuesday.
	at := func(hour, min int) time.Time { return time.Date(2026, 11, 10, hour, min, 0, 0, time.Local) }
	tests := []struct {
		name       string
		start, end time.Time
		want       string
	}{
		{"started 5 minutes early, in the class before", at(12, 55), at(15, 2), "컴퓨터구조"},
		{"ran 5 minutes into the next class", at(10, 52), at(13, 5), "논리회로"},
		{"started 15 minutes early with a gap before", at(15, 45), at(17, 20), "선형대수학"},
		{"short, mostly in the second class", at(12, 58), at(13, 30), "컴퓨터구조"},
		{"tie across the break", at(12, 50), at(13, 10), ""},
		{"only a few minutes of a class", at(9, 0), at(11, 5), ""},
		{"no class", at(18, 0), at(19, 0), ""},
		{"other day", at(12, 55).AddDate(0, 0, 1), at(15, 2).AddDate(0, 0, 1), ""},
		{"length unknown, on the line between back-to-back classes", at(13, 0), at(13, 0), ""},
		{"past midnight, cut there", at(21, 55), at(21, 55).Add(135 * time.Minute), "야간세미나"},
	}
	for _, tt := range tests {
		got, ok := ByTime(courses, tt.start, tt.end)
		if got.Name != tt.want || ok != (tt.want != "") {
			t.Errorf("%s: ByTime = %q, %v; want %q", tt.name, got.Name, ok, tt.want)
		}
	}
}
