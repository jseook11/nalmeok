package course

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/i18n"
)

func TestSampleTakesFiveSpots(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "w%02d ", i) // 4 runes per word, word i near 4i
	}
	got := Sample(b.String(), 50)
	parts := strings.Split(got, " ... ")
	if len(parts) != 5 {
		t.Fatalf("parts = %q", got)
	}
	for i, want := range []string{"w20", "w35", "w50", "w65", "w80"} {
		if !strings.Contains(parts[i], want) {
			t.Errorf("part %d = %q, want it around %s", i, parts[i], want)
		}
		for _, w := range strings.Fields(parts[i]) {
			if len(w) != 3 {
				t.Errorf("split word %q", w)
			}
		}
	}
	if Sample("짧음", 100) != "짧음" {
		t.Error("short text changed")
	}
	if n := utf8.RuneCountInString(Sample(strings.Repeat("가나 ", 2000), SampleRunes)); n > SampleRunes+30 {
		t.Errorf("sample of %d runes", n)
	}
}

func TestCleanTopic(t *testing.T) {
	cases := map[string]string{
		"부분공간과 생성.":             "부분공간과 생성",
		"A/B: 정렬?":              "A B 정렬",
		"  여러   공백  ":           "여러 공백",
		strings.Repeat("가", 40): strings.Repeat("가", 30),
	}
	for in, want := range cases {
		if got := cleanTopic(in); got != want {
			t.Errorf("cleanTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileBase(t *testing.T) {
	if FileBase(0, "정렬") != "정렬" || FileBase(0, "") != "" {
		t.Error("no week should give the topic alone")
	}
	// Both parts follow the interface language, like the rest of the app.
	if got := FileBase(10, "basis와 eigenvector"); got != i18n.T("course.weekTopic", "week", 10, "topic", "basis와 eigenvector") {
		t.Errorf("FileBase = %q", got)
	}
	if got := FileBase(3, ""); got != i18n.T("course.week", "week", 3) {
		t.Errorf("FileBase = %q", got)
	}
}

func TestWeekFromName(t *testing.T) {
	cases := map[string]int{
		"프로그래밍언어론 9주차-2":            9,
		"선형대수학 10주차":                10,
		"선형대수학 7주차 중간고사 Review (2)": 7,
		"Week 12 lecture":           12,
		"week3":                     3,
		"w9 notes":                  9,
		"alg10":                     0,
		"녹음 23":                     0,
		"2026-11-10":                0,
		"99주차":                      0,
	}
	for name, want := range cases {
		if got := WeekFromName(name); got != want {
			t.Errorf("WeekFromName(%q) = %d, want %d", name, got, want)
		}
	}
}
