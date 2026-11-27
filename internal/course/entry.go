package course

import (
	"slices"
	"strings"
	"unicode"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
)

// Entry is one line typed into the timetable's add field: a lecture and the
// class times written after (or before) it.
type Entry struct {
	Name  string
	Slots []config.Slot
}

// ParseEntry reads a lecture and its weekly class times from what a student
// would type, in Korean or English:
//
//	프로그래밍, 화 1500 ~ 1900      프로그래밍 화15001900
//	프로그래밍 화15:00~19:00        논리회로 월 11-13, 수 11-12
//	자료구조 화목 10:30-11:45       선형대수 월/수 9시~10시 15분
//	알고리즘 화요일 오후 3시~5시 반   Algorithms Tue/Thu 3pm-4:15pm
//	회로실험 금 13:00 3시간          English Mon, Wed 9:00 to 10:15
//
// A day group is one or more days (화목, 화/목, Tue & Thu); each group is
// followed by a time range: start and end as 15:00, 1500, 15시 30분, 3시 반,
// 3pm, with -, ~, to, 부터…까지 or a space between, or a length (3시간,
// 90분). Hours 1 to 7 without 오전/am are afternoon classes; an end not after
// the start moves to the afternoon (11-1 is 11:00–13:00). Whatever is not a
// day or a time is the lecture's name.
func ParseEntry(text string) (Entry, error) {
	// s is matched against; orig, the same runes before lower-casing,
	// gives the name its own spelling.
	orig := []rune(normalize(text))
	s := make([]rune, len(orig))
	for i, r := range orig {
		s[i] = unicode.ToLower(r)
	}
	var slots []config.Slot
	used := make([]bool, len(s))
	sawDay := false
	// A day left without a readable time ("월 11-13 수", "수 25-26") makes
	// the line wrong; it must not end up in the name. A day word before
	// other words is the name's ("토요 세미나" stays whole).
	timeless := false
	for i := 0; i < len(s); {
		if !dayBoundary(s, i) {
			i++
			continue
		}
		days, after := scanDays(s, i)
		if len(days) == 0 {
			i++
			continue
		}
		sawDay = true
		gap := skipGap(s, after)
		start, end, next, ok := scanRange(s, gap)
		if !ok {
			if gap == len(s) || unicode.IsDigit(s[gap]) {
				timeless = true
			}
			i = after
			continue
		}
		for {
			for _, d := range days {
				slot := config.Slot{Day: d, Start: clock(start), End: clock(end)}
				if !slices.Contains(slots, slot) {
					slots = append(slots, slot)
				}
			}
			// More times for the same days: 월 9-10, 13-14.
			j := next
			for j < len(s) && (s[j] == ' ' || s[j] == ',') {
				j++
			}
			if d, _ := dayAt(s, j); d != 0 {
				break
			}
			if start, end, j, ok = scanRange(s, j); !ok {
				break
			}
			next = j
		}
		for k := i; k < next; k++ {
			used[k] = true
		}
		i = next
	}
	name := leftover(orig, used)
	switch {
	case (len(slots) == 0 && sawDay) || timeless:
		return Entry{}, i18n.Error("error.entryTime")
	case len(slots) == 0:
		return Entry{}, i18n.Error("error.entryFormat")
	case name == "":
		return Entry{}, i18n.Error("error.entryName")
	}
	// Times within the line must not overlap each other (화 9-11, 10-12).
	if o := config.Overlaps([]config.Course{{Name: name, Slots: slots}}); len(o) > 0 {
		return Entry{}, i18n.Error("error.entryOverlap", "a", o[0].SA.Start+"–"+o[0].SA.End, "b", o[0].SB.Start+"–"+o[0].SB.End)
	}
	return Entry{Name: name, Slots: slots}, nil
}

// normalize folds what keyboards and phones produce into one spelling:
// full-width forms, the many dashes and tildes. Each rune maps to one rune,
// so positions stay the same.
func normalize(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= '０' && r <= '９':
			r = '0' + (r - '０')
		case r >= 'Ａ' && r <= 'Ｚ':
			r = 'a' + (r - 'Ａ')
		case r >= 'ａ' && r <= 'ｚ':
			r = 'a' + (r - 'ａ')
		case r == '：':
			r = ':'
		case r == '，' || r == '、':
			r = ','
		case r == '／':
			r = '/'
		case strings.ContainsRune("～〜∼~", r):
			r = '~'
		case strings.ContainsRune("‐‑‒–—―−-", r):
			r = '-'
		case unicode.IsSpace(r):
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Day words, longest first so "tuesday" wins over "tue".
var dayWords = []struct {
	word string
	day  int
}{
	{"월요일", 1}, {"화요일", 2}, {"수요일", 3}, {"목요일", 4}, {"금요일", 5}, {"토요일", 6}, {"일요일", 7},
	{"wednesday", 3}, {"thursday", 4}, {"saturday", 6}, {"tuesday", 2},
	{"monday", 1}, {"friday", 5}, {"sunday", 7},
	{"thurs", 4}, {"tues", 2}, {"thur", 4}, {"thu", 4}, {"tue", 2}, {"mon", 1}, {"wed", 3}, {"fri", 5}, {"sat", 6}, {"sun", 7},
	{"월", 1}, {"화", 2}, {"수", 3}, {"목", 4}, {"금", 5}, {"토", 6}, {"일", 7},
}

func hasAt(s []rune, i int, word string) bool {
	w := []rune(word)
	if i+len(w) > len(s) {
		return false
	}
	for k, r := range w {
		if s[i+k] != r {
			return false
		}
	}
	return true
}

func dayAt(s []rune, i int) (day, n int) {
	for _, d := range dayWords {
		if !hasAt(s, i, d.word) {
			continue
		}
		n = len([]rune(d.word))
		// An English day must end at a word boundary ("mon" in "monitor"
		// is not Monday).
		if d.word[0] < 0x80 && i+n < len(s) && unicode.IsLetter(s[i+n]) {
			continue
		}
		return d.day, n
	}
	return 0, 0
}

// dayBoundary: a day group starts the text or follows a space or a
// separator, so the 수 in 선형대수 is not Wednesday.
func dayBoundary(s []rune, i int) bool {
	return i == 0 || !unicode.IsLetter(s[i-1]) && !unicode.IsDigit(s[i-1])
}

// scanDays reads 화목, 화/목, 화, 목, tue & thu, mon wed (English days may
// also be split by spaces). It returns the days in order without repeats.
func scanDays(s []rune, i int) ([]int, int) {
	var days []int
	seen := map[int]bool{}
	end := i
	for {
		d, n := dayAt(s, i)
		if d == 0 {
			break
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
		i += n
		end = i
		// Separators between days: 화목 (none), 화/목, 화,목, 화·목, 화&목, 화 및 목,
		// "mon wed".
		j := i
		for j < len(s) && (s[j] == ' ' || strings.ContainsRune("/,·.&+", s[j])) {
			j++
		}
		for _, word := range []string{"및 ", "and ", "와 ", "과 "} {
			if hasAt(s, j, word) {
				j += len([]rune(word))
			}
		}
		if next, _ := dayAt(s, j); next == 0 {
			break
		}
		i = j
	}
	return days, end
}

// skipGap passes what may sit between the days and the time: spaces, a
// comma or colon, "에", "매주".
func skipGap(s []rune, i int) int {
	for i < len(s) {
		switch {
		case s[i] == ' ' || s[i] == ',' || s[i] == ':':
			i++
		case hasAt(s, i, "에 "):
			i += 2
		default:
			return i
		}
	}
	return i
}

// meridiem marks: 0 none, 'a' morning, 'p' afternoon.
func scanMeridiem(s []rune, i int) (byte, int) {
	for _, m := range []struct {
		word string
		mark byte
	}{
		{"오전", 'a'}, {"오후", 'p'}, {"아침", 'a'}, {"낮", 'p'}, {"저녁", 'p'}, {"밤", 'p'},
		{"a.m.", 'a'}, {"p.m.", 'p'}, {"am", 'a'}, {"pm", 'p'},
	} {
		if hasAt(s, i, m.word) {
			n := len([]rune(m.word))
			// "am"/"pm" must not run into a word ("amazing").
			if m.word[0] < 0x80 && i+n < len(s) && unicode.IsLetter(s[i+n]) {
				continue
			}
			return m.mark, i + n
		}
	}
	return 0, i
}

func digits(s []rune, i int) (string, int) {
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return string(s[i:j]), j
}

func spaces(s []rune, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return i
}

func atoi(d string) int {
	n := 0
	for _, r := range d {
		n = n*10 + int(r-'0')
	}
	return n
}

// timeOf is one written time before meridiem rules: hour and minute as
// written, its 오전/오후 mark, and whether it was 24-hour on its face
// (13:00, 0900).
type timeOf struct {
	hour, min int
	mark      byte
	fixed     bool
}

// scanTime reads 15:00, 15.00, 1500, 900, 15시, 15시 30분, 3시 반, 3pm, 오후 3:30,
// 15h30. compact is the digit run when it was 5 to 8 digits long (a start
// and end written together, 15001900), for the caller to split.
func scanTime(s []rune, i int) (t timeOf, next int, compact string, ok bool) {
	mark, i := scanMeridiem(s, i)
	i = spaces(s, i)
	d, j := digits(s, i)
	switch {
	case len(d) == 0:
		return t, i, "", false
	case len(d) >= 5 && len(d) <= 8:
		return t, j, d, true
	case len(d) > 8:
		return t, i, "", false
	case len(d) >= 3: // 1500, 900
		t = timeOf{hour: atoi(d[:len(d)-2]), min: atoi(d[len(d)-2:]), fixed: len(d) == 4}
	default:
		t = timeOf{hour: atoi(d), fixed: len(d) == 2 && (d[0] == '0' || atoi(d) >= 13)}
		switch {
		case j < len(s) && (s[j] == ':' || s[j] == '.' || s[j] == 'h') && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9':
			m, k := digits(s, j+1)
			if len(m) != 2 {
				return t, i, "", false
			}
			t.min, j = atoi(m), k
			t.fixed = t.fixed || t.hour >= 13
		case j < len(s) && (s[j] == '시' || s[j] == 'h'):
			j++
			k := spaces(s, j)
			if m, k2 := digits(s, k); m != "" && k2 < len(s) && s[k2] == '분' {
				t.min, j = atoi(m), k2+1
			} else if hasAt(s, k, "반") {
				t.min, j = 30, k+1
			}
		}
	}
	if after, k := scanMeridiem(s, spaces(s, j)); after != 0 {
		mark, j = after, k
	}
	t.mark = mark
	return t, j, "", t.hour <= 24 && t.min < 60
}

// scanDuration reads 3시간, 1시간 15분, 90분, 75min, 1h 15m, 2 hours.
func scanDuration(s []rune, i int) (int, int, bool) {
	total, found := 0, false
	for {
		k := spaces(s, i)
		d, j := digits(s, k)
		if d == "" {
			break
		}
		j = spaces(s, j)
		unit := 0
		for _, u := range []struct {
			word string
			mins int
		}{{"시간", 60}, {"hours", 60}, {"hour", 60}, {"hrs", 60}, {"hr", 60}, {"h", 60}, {"분", 1}, {"minutes", 1}, {"mins", 1}, {"min", 1}, {"m", 1}} {
			if hasAt(s, j, u.word) {
				unit, j = u.mins, j+len([]rune(u.word))
				break
			}
		}
		if unit == 0 {
			break
		}
		total += atoi(d) * unit
		found, i = true, j
	}
	return total, i, found && total > 0
}

// scanRange reads a start and an end (or a length) at i.
func scanRange(s []rune, i int) (start, end, next int, ok bool) {
	a, j, compact, ok := scanTime(s, i)
	if !ok {
		return 0, 0, i, false
	}
	if compact != "" {
		return splitCompact(compact, j)
	}
	j = spaces(s, j)
	sep := false
	for _, w := range []string{"부터", "에서", "to", "until", "till", "-", "~"} {
		if hasAt(s, j, w) {
			j, sep = spaces(s, j+len([]rune(w))), true
			break
		}
	}
	if mins, k, isLen := scanDuration(s, j); isLen && (sep || !startsTime(s, j)) {
		st, ok := resolveStart(a, nil)
		if !ok || st+mins >= 24*60 || mins > 12*60 {
			return 0, 0, i, false
		}
		return st, st + mins, eatUntil(s, k), true
	}
	b, k, compactEnd, ok := scanTime(s, j)
	if !ok || compactEnd != "" || (!sep && k == j) {
		return 0, 0, i, false
	}
	// "1500 1900" may go without a separator; "3 5" may not.
	if !sep && !(a.fixed && b.fixed) {
		return 0, 0, i, false
	}
	start, end, ok = resolve(a, b)
	if !ok {
		return 0, 0, i, false
	}
	return start, end, eatUntil(s, k), true
}

// startsTime reports whether a time (not a length) starts at i: digits not
// followed by a duration unit.
func startsTime(s []rune, i int) bool {
	_, _, isLen := scanDuration(s, i)
	return !isLen
}

// eatUntil passes a trailing 까지 / 까지만.
func eatUntil(s []rune, i int) int {
	if hasAt(s, i, "까지") {
		return i + 2
	}
	return i
}

// splitCompact reads start and end written as one run: 15001900, 9001015.
func splitCompact(d string, next int) (int, int, int, bool) {
	for _, cut := range []int{4, 3} {
		if cut >= len(d) || len(d)-cut < 3 || len(d)-cut > 4 {
			continue
		}
		a := timeOf{hour: atoi(d[:cut-2]), min: atoi(d[cut-2 : cut]), fixed: cut == 4}
		b := timeOf{hour: atoi(d[cut : len(d)-2]), min: atoi(d[len(d)-2:]), fixed: len(d)-cut == 4}
		if a.hour > 24 || b.hour > 24 || a.min > 59 || b.min > 59 {
			continue
		}
		if start, end, ok := resolve(a, b); ok {
			return start, end, next, true
		}
	}
	return 0, 0, next, false
}

// resolveStart turns a written start into minutes. With a mark it is as
// marked; without one, 1 to 7 o'clock is an afternoon class.
func resolveStart(t timeOf, _ *timeOf) (int, bool) {
	h := t.hour
	switch {
	case t.mark == 'p' && h < 12:
		h += 12
	case t.mark == 'a' && h == 12:
		h = 0
	case t.mark == 0 && !t.fixed && h >= 1 && h <= 7:
		h += 12
	}
	m := h*60 + t.min
	return m, m < 24*60
}

// resolve turns a written start and end into minutes, sharing a mark
// written once (오후 3시~5시, 3-5pm) and moving an end that is not after the
// start into the afternoon (11-1).
func resolve(a, b timeOf) (int, int, bool) {
	switch {
	case a.mark != 0 && b.mark == 0 && !b.fixed:
		b.mark = a.mark
		if mins(b) <= mins(a) && a.mark == 'a' {
			b.mark = 'p' // 오전 11시~1시
		}
	case a.mark == 0 && b.mark != 0 && !a.fixed:
		a.mark = b.mark
		if mins(a) >= mins(b) {
			a.mark = 'a' // 11-1pm
		}
	}
	start, ok := resolveStart(a, nil)
	if !ok {
		return 0, 0, false
	}
	end := mins(b)
	if b.mark == 0 && !b.fixed && end <= start && b.hour < 12 {
		end += 12 * 60
	}
	if end <= start || end >= 24*60 || end-start > 12*60 {
		return 0, 0, false
	}
	return start, end, true
}

// mins is t in minutes after its mark, without the afternoon guess.
func mins(t timeOf) int {
	h := t.hour
	if t.mark == 'p' && h < 12 {
		h += 12
	}
	if t.mark == 'a' && h == 12 {
		h = 0
	}
	return h*60 + t.min
}

func clock(m int) string {
	return string([]rune{rune('0' + m/600), rune('0' + m/60%10), ':', rune('0' + m%60/10), rune('0' + m%10)})
}

// leftover is the name: the text outside the matched days and times, with
// the separators around them trimmed and "매주" dropped.
func leftover(s []rune, used []bool) string {
	var b strings.Builder
	for i, r := range s {
		if used[i] {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	name := strings.NewReplacer("매주", " ", "every ", " ", "Every ", " ").Replace(b.String())
	name = strings.Join(strings.Fields(name), " ")
	trim := " ,.;:/|·-~()[]{}"
	for {
		before := name
		name = strings.Trim(name, trim)
		name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(name, ","), " ,"))
		name = strings.ReplaceAll(name, " ,", ",")
		if name == before {
			break
		}
	}
	return strings.Trim(strings.Join(strings.Fields(strings.ReplaceAll(name, ",,", ",")), " "), trim)
}
