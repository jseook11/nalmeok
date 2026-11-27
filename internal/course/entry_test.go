package course

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
)

func slotsText(slots []config.Slot) string {
	var parts []string
	for _, s := range slots {
		parts = append(parts, fmt.Sprintf("%d %s-%s", s.Day, s.Start, s.End))
	}
	return strings.Join(parts, ", ")
}

func TestParseEntry(t *testing.T) {
	tests := []struct{ in, name, slots string }{
		// The forms asked for first.
		{"프로그래밍, 화 1500 ~ 1900", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍, 화15001900", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화15:00~19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍, 월 15001800", "프로그래밍", "1 15:00-18:00"},
		// A day word followed by words belongs to the name.
		{"Sun Lab Mon 9-10", "Sun Lab", "1 09:00-10:00"},
		// Separators and spacing.
		{"프로그래밍 화 15:00-19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15:00 - 19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 1500 1900", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15:00–19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15:00〜19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍　화　１５：００～１９：００", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15.00-19.00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15:00 to 19:00", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 화 15시부터 19시까지", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍: 화 15-19", "프로그래밍", "2 15:00-19:00"},
		{"프로그래밍 (화 15-19)", "프로그래밍", "2 15:00-19:00"},
		// Times on the slot's days.
		{"자료구조 화목 10:30-11:45", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"자료구조 화/목 1030-1145", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"자료구조 화, 목 10:30~11:45", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"자료구조 화·목 10:30~11:45", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"자료구조 화요일 목요일 10:30~11:45", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"논리회로 월1100-1300 수1100-1200", "논리회로", "1 11:00-13:00, 3 11:00-12:00"},
		{"논리회로, 화 1100-1300, 목 1200-1300", "논리회로", "2 11:00-13:00, 4 12:00-13:00"},
		{"논리회로 월 9-10, 13-14", "논리회로", "1 09:00-10:00, 1 13:00-14:00"},
		{"매주 화 15-19 프로그래밍", "프로그래밍", "2 15:00-19:00"},
		{"화 15-19 프로그래밍", "프로그래밍", "2 15:00-19:00"},
		// Korean hours and minutes.
		{"선형대수 월/수 9시~10시 15분", "선형대수", "1 09:00-10:15, 3 09:00-10:15"},
		{"알고리즘 화요일 오후 3시~5시 반", "알고리즘", "2 15:00-17:30"},
		{"알고리즘 화 오후 3:00 - 오후 4:15", "알고리즘", "2 15:00-16:15"},
		{"알고리즘 화 오전 11시~1시", "알고리즘", "2 11:00-13:00"},
		{"알고리즘 화 3시 반-5시", "알고리즘", "2 15:30-17:00"},
		// 12-hour guesses.
		{"알고리즘 화 3-6", "알고리즘", "2 15:00-18:00"},
		{"알고리즘 화 11-1", "알고리즘", "2 11:00-13:00"},
		{"알고리즘 화 12-1", "알고리즘", "2 12:00-13:00"},
		{"알고리즘 화 9-10:15", "알고리즘", "2 09:00-10:15"},
		{"알고리즘 화 0700-0850", "알고리즘", "2 07:00-08:50"},
		{"알고리즘 화 7-9", "알고리즘", "2 19:00-21:00"},
		{"알고리즘 화 9001015", "알고리즘", "2 09:00-10:15"},
		// English.
		{"Algorithms Tue/Thu 3pm-4:15pm", "Algorithms", "2 15:00-16:15, 4 15:00-16:15"},
		{"Algorithms Tue/Thu 3-4:15pm", "Algorithms", "2 15:00-16:15, 4 15:00-16:15"},
		{"English Mon, Wed 9:00 to 10:15", "English", "1 09:00-10:15, 3 09:00-10:15"},
		{"English Monday and Wednesday 9:00 AM - 10:15 AM", "English", "1 09:00-10:15, 3 09:00-10:15"},
		{"Physics tues 11am-1pm", "Physics", "2 11:00-13:00"},
		{"Physics TUESDAY 1500-1900", "Physics", "2 15:00-19:00"},
		// A length instead of an end.
		{"회로실험 금 13:00 3시간", "회로실험", "5 13:00-16:00"},
		{"회로실험 금 13:00부터 1시간 30분", "회로실험", "5 13:00-14:30"},
		{"회로실험 금 13:00 90분", "회로실험", "5 13:00-14:30"},
		{"Lab fri 1pm 2h", "Lab", "5 13:00-15:00"},
		// No commas anywhere.
		{"프로그래밍 화 1500 1900", "프로그래밍", "2 15:00-19:00"},
		{"논리회로 월 11-13 수 11-12", "논리회로", "1 11:00-13:00, 3 11:00-12:00"},
		{"논리회로 화 1100 1300 목 1200 1300", "논리회로", "2 11:00-13:00, 4 12:00-13:00"},
		{"논리회로 월 9-10 13-14", "논리회로", "1 09:00-10:00, 1 13:00-14:00"},
		{"프로그래밍 언어론 화 11-13 목 12-13", "프로그래밍 언어론", "2 11:00-13:00, 4 12:00-13:00"},
		{"자료구조 화 목 10:30-11:45", "자료구조", "2 10:30-11:45, 4 10:30-11:45"},
		{"English Mon Wed 9:00-10:15", "English", "1 09:00-10:15, 3 09:00-10:15"},
		// Day letters inside names are not days.
		{"선형대수 월 9-10", "선형대수", "1 09:00-10:00"},
		{"화학 실험 목 13-15", "화학 실험", "4 13:00-15:00"},
		{"일반물리 월 9-10", "일반물리", "1 09:00-10:00"},
		{"수학 일 9-10", "수학", "7 09:00-10:00"},
		{"Monitoring mon 9-10", "Monitoring", "1 09:00-10:00"},
		{"프로그래밍 언어론, 화 11-13", "프로그래밍 언어론", "2 11:00-13:00"},
		{"C++ 프로그래밍 월 9-10", "C++ 프로그래밍", "1 09:00-10:00"},
	}
	for _, tt := range tests {
		got, err := ParseEntry(tt.in)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if got.Name != tt.name || slotsText(got.Slots) != tt.slots {
			t.Errorf("%q = %q [%s]; want %q [%s]", tt.in, got.Name, slotsText(got.Slots), tt.name, tt.slots)
		}
	}
}

func TestParseEntryRejects(t *testing.T) {
	for _, tt := range []struct{ in, key string }{
		{"", "error.entryFormat"},
		{"프로그래밍", "error.entryFormat"},
		{"프로그래밍 15-19", "error.entryFormat"},
		{"프로그래밍 화", "error.entryTime"},
		{"프로그래밍 화 19-15", "error.entryTime"},
		{"프로그래밍 화 25:00-26:00", "error.entryTime"},
		{"프로그래밍 화 15:70-19:00", "error.entryTime"},
		{"프로그래밍 화 3 5", "error.entryTime"},
		{"화 15-19", "error.entryName"},
		{"화목 1500 1900", "error.entryName"},
		// One good day and one without a readable time: refused, not
		// folded into the name.
		{"Math Mon 09:00-10:00 Tue 25:00-26:00", "error.entryTime"},
		{"논리회로 월 11-13 수 2500-2600", "error.entryTime"},
		{"논리회로 월 11-13 수", "error.entryTime"},
	} {
		_, err := ParseEntry(tt.in)
		if err == nil || !strings.Contains(fmt.Sprintf("%#v", err)+err.Error(), tt.key) {
			t.Errorf("%q: err = %v; want %s", tt.in, err, tt.key)
		}
	}
}

func TestParseEntryOverlap(t *testing.T) {
	if _, err := ParseEntry("논리회로 화 9-11, 10-12"); err == nil {
		t.Error("overlapping times in one line accepted")
	}
	if _, err := ParseEntry("논리회로 화 9-11, 11-12"); err != nil {
		t.Errorf("back-to-back times refused: %v", err)
	}
	if e, err := ParseEntry("논리회로 화 9-11, 화 9-11"); err != nil || len(e.Slots) != 1 {
		t.Errorf("a repeated time: %+v, %v", e, err)
	}
	if _, err := ParseEntry("야간 월 23:00-24:00"); err == nil {
		t.Error("an end at midnight accepted")
	}
}
