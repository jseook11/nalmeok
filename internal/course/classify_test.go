package course

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSampleTakesThreeMiddleSpots(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "w%02d ", i) // 4 runes per word, word i near 4i
	}
	got := Sample(b.String(), 30)
	parts := strings.Split(got, " ... ")
	if len(parts) != 3 {
		t.Fatalf("parts = %q", got)
	}
	for i, want := range []string{"w30", "w50", "w70"} {
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
	if !strings.Contains(FileBase(3, ""), "3") || !strings.Contains(FileBase(3, "정렬"), "정렬") {
		t.Error("week names lost a part")
	}
}
