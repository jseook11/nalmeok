package app

import (
	"context"
	"encoding/json"
	"golang.org/x/text/unicode/norm"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/course"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
	"google.golang.org/genai"
)

// fakeSorter answers every classify request with one of answers, by the
// transcript it sees, and records which key asked.
type fakeSorter struct {
	gemini.Client
	key     string
	answers map[string]string // substring of the excerpt -> JSON answer
	asked   *[]string
	err     error
}

func (f *fakeSorter) Generate(_ context.Context, req gemini.Request) (gemini.Result, error) {
	*f.asked = append(*f.asked, f.key)
	if f.err != nil {
		return gemini.Result{}, f.err
	}
	if req.Schema == nil || req.Model == "" {
		panic("classify request without a schema or model")
	}
	for k, v := range f.answers {
		if strings.Contains(req.Parts[0].Text, k) {
			return gemini.Result{Text: v}, nil
		}
	}
	return gemini.Result{Text: `{"course":"NONE","confident":false,"topic":""}`}, nil
}

// sortFixture makes a lecture recorded on 2026-11-10 (week 11 of a
// semester starting Tuesday 2026-09-01) with its finished transcript.
func sortFixture(t *testing.T, transcript string) (*testApp, store.Item) {
	t.Helper()
	ta := newTestApp(t)
	src := filepath.Join(ta.dir, "녹음 23.m4a")
	if err := os.WriteFile(src, []byte("not really audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := time.Date(2026, 11, 10, 13, 0, 0, 0, time.Local)
	if err := os.Chtimes(src, rec, rec); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(ta.app.OutputDir, "녹음 23.md")
	if err := os.MkdirAll(ta.app.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	item := store.Item{ID: "i1", Source: src, Kind: "file", Name: "녹음 23", State: "done", Output: out}
	if err := ta.app.store.Add(item); err != nil {
		t.Fatal(err)
	}
	return ta, item
}

func (ta *testApp) withSorter(t *testing.T, answers map[string]string, errs map[string]error) *[]string {
	t.Helper()
	asked := &[]string{}
	ta.app.NewClient = func(key string) (gemini.Client, error) {
		return &fakeSorter{key: key, answers: answers, asked: asked, err: errs[key]}, nil
	}
	return asked
}

func (ta *testApp) setCourses(t *testing.T, start string, names ...string) {
	t.Helper()
	var list []config.Course
	for _, n := range names {
		list = append(list, config.Course{Name: n})
	}
	if err := ta.app.SetCourses(CourseSettings{Courses: list, SemesterStart: start}); err != nil {
		t.Fatal(err)
	}
}

func TestSortCoursesMovesIntoLectureFolder(t *testing.T) {
	ta, item := sortFixture(t, strings.Repeat("고유값 ", 400)+"부분공간 span basis "+strings.Repeat("벡터 ", 400))
	ta.setCourses(t, "", "선형대수학", "알고리즘")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	ta.withSorter(t, map[string]string{"부분공간": `{"course":"선형대수학","confident":true,"topic":"부분공간과 생성."}`}, nil)

	if n := ta.app.SortableCount(); n != 1 {
		t.Fatalf("SortableCount = %d", n)
	}
	sum, err := ta.app.SortCourses()
	if err != nil || sum.Matched != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	got, _ := ta.app.store.Get(item.ID)
	base := course.FileBase(11, "부분공간과 생성")
	want := filepath.Join(ta.app.OutputDir, "선형대수학", base+".md")
	if got.Output != want || got.Name != base || got.Course != "선형대수학" || got.Sorted != sortedMatched {
		t.Fatalf("item = %q %q %q %q", got.Output, got.Name, got.Course, got.Sorted)
	}
	if !isFile(want) || isFile(item.Output) {
		t.Fatal("transcript was not moved")
	}
	if n := ta.app.SortableCount(); n != 0 {
		t.Errorf("SortableCount after sorting = %d", n)
	}
}

func TestSortCoursesLeavesUnsureAlone(t *testing.T) {
	ta, item := sortFixture(t, "출석 부르겠습니다")
	ta.setCourses(t, "", "선형대수학")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	ta.withSorter(t, map[string]string{"출석": `{"course":"선형대수학","confident":false,"topic":"출석"}`}, nil)
	sum, err := ta.app.SortCourses()
	if err != nil || sum.None != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	got, _ := ta.app.store.Get(item.ID)
	if got.Output != item.Output || got.Sorted != sortedNone || got.Course != "" {
		t.Fatalf("item = %+v", got)
	}
	// The next run skips it; sorting it alone asks again.
	if n := ta.app.SortableCount(); n != 0 {
		t.Errorf("SortableCount = %d", n)
	}
}

func TestSortCourseAgainMovesBetweenLectures(t *testing.T) {
	ta, item := sortFixture(t, "정렬 알고리즘 그리고 부분공간")
	ta.setCourses(t, "2026-09-07", "선형대수학", "알고리즘")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	answers := map[string]string{"부분공간": `{"course":"선형대수학","confident":true,"topic":"부분공간"}`}
	ta.withSorter(t, answers, nil)
	if _, err := ta.app.SortCourses(); err != nil {
		t.Fatal(err)
	}
	// Week counts from the set start (Monday 2026-09-07): 2026-11-10 is week 10.
	first, _ := ta.app.store.Get(item.ID)
	if filepath.Base(first.Output) != course.FileBase(10, "부분공간")+".md" {
		t.Fatalf("first = %s", first.Output)
	}
	answers["부분공간"] = `{"course":"알고리즘","confident":true,"topic":"정렬"}`
	if _, err := ta.app.SortCourse(item.ID); err != nil {
		t.Fatal(err)
	}
	again, _ := ta.app.store.Get(item.ID)
	if want := filepath.Join(ta.app.OutputDir, "알고리즘", course.FileBase(10, "정렬")+".md"); again.Output != want {
		t.Fatalf("again = %s, want %s", again.Output, want)
	}
}

func TestSortCoursesTriesNextKeyOn429(t *testing.T) {
	ta, item := sortFixture(t, "부분공간")
	ta.setCourses(t, "", "선형대수학")
	paid := "AIza" + strings.Repeat("p", 35)
	free := "AIza" + strings.Repeat("f", 35)
	if err := ta.app.AddKey("", "paid", paid); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.AddKey("", "free", free); err != nil {
		t.Fatal(err)
	}
	asked := ta.withSorter(t, map[string]string{"부분공간": `{"course":"선형대수학","confident":true,"topic":"부분공간"}`},
		map[string]error{free: genai.APIError{Code: 429}})
	sum, err := ta.app.SortCourses()
	if err != nil || sum.Matched != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	if strings.Join(*asked, ",") != free+","+paid {
		t.Errorf("asked = %v, want the free key first, then paid", *asked)
	}
	if got, _ := ta.app.store.Get(item.ID); got.Course != "선형대수학" {
		t.Errorf("Course = %q", got.Course)
	}
}

func TestSortCoursesNeedsCourses(t *testing.T) {
	ta, _ := sortFixture(t, "x")
	if _, err := ta.app.SortCourses(); err == nil {
		t.Fatal("sorting without a lecture list succeeded")
	}
}

func TestSetCoursesRejectsBadInput(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.SetCourses(CourseSettings{SemesterStart: "9월 1일"}); err == nil {
		t.Error("bad date accepted")
	}
	if err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{{Name: "a/b"}}}); err == nil {
		t.Error("slash in a lecture name accepted")
	}
	err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{{Name: " 선형대수학 "}, {Name: "선형대수학"}, {Name: ""}, {Name: "알고리즘"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := ta.app.Courses()
	if len(got.Courses) != 2 || got.Courses[0].Name != "선형대수학" || got.Courses[1].Name != "알고리즘" {
		t.Fatalf("Courses = %+v", got)
	}
}

func TestSortUsesTheRecordingName(t *testing.T) {
	ta, item := sortFixture(t, "오늘은 출석만 부르고 끝")
	ta.setCourses(t, "", "프로그래밍", "프로그래밍언어론")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	// The name reaches the classifier. The week still comes from when it
	// was recorded (week 11), not from the name.
	src := filepath.Join(ta.dir, "프로그래밍언어론 9주차-2.m4a")
	if err := os.Rename(item.Source, src); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.store.Update(item.ID, func(it *store.Item) { it.Source = src }); err != nil {
		t.Fatal(err)
	}
	ta.withSorter(t, map[string]string{"File name: 프로그래밍언어론 9주차-2\n": `{"course":"프로그래밍언어론","confident":true,"topic":"자료형"}`}, nil)
	if sum, err := ta.app.SortCourses(); err != nil || sum.Matched != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	got, _ := ta.app.store.Get(item.ID)
	if want := filepath.Join(ta.app.OutputDir, "프로그래밍언어론", course.FileBase(11, "자료형")+".md"); got.Output != want {
		t.Fatalf("output = %s, want %s", got.Output, want)
	}
}

func TestSortFolderGoesIntoTranscriptFolder(t *testing.T) {
	ta, item := sortFixture(t, "부분공간 span basis")
	ta.setCourses(t, "", "선형대수학")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	// A folder item's transcript lives inside the recording folder.
	folder := filepath.Join(ta.dir, "선형 10주차")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(folder, "선형 10주차.md")
	if err := os.Rename(item.Output, out); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.store.Update(item.ID, func(it *store.Item) { it.Kind, it.Source, it.Output = "folder", folder, out }); err != nil {
		t.Fatal(err)
	}
	// The same folder added again shares the transcript.
	dup, _ := ta.app.store.Get(item.ID)
	dup.ID = "i2"
	if err := ta.app.store.Add(dup); err != nil {
		t.Fatal(err)
	}
	// An item whose transcript is gone is not counted.
	gone := dup
	gone.ID, gone.Output = "i3", filepath.Join(ta.dir, "gone", "gone.md")
	if err := ta.app.store.Add(gone); err != nil {
		t.Fatal(err)
	}
	if n := ta.app.SortableCount(); n != 2 {
		t.Fatalf("SortableCount = %d, want 2", n)
	}
	ta.withSorter(t, map[string]string{"부분공간": `{"course":"선형대수학","confident":true,"topic":"부분공간"}`}, nil)
	if sum, err := ta.app.SortCourses(); err != nil || sum.Matched != 1 || sum.Failed != 0 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	want := filepath.Join(ta.app.OutputDir, "선형대수학", course.FileBase(10, "부분공간")+".md")
	for _, id := range []string{"i1", "i2"} {
		got, _ := ta.app.store.Get(id)
		if got.Output != want || got.Sorted != sortedMatched || got.Course != "선형대수학" {
			t.Errorf("%s = %q %q", id, got.Output, got.Sorted)
		}
	}
	if !isFile(want) {
		t.Error("transcript was not moved into the transcript folder")
	}
	if n := ta.app.SortableCount(); n != 0 {
		t.Errorf("SortableCount after = %d", n)
	}
}

func TestWeekFallsBackToTheName(t *testing.T) {
	ta, item := sortFixture(t, "부분공간")
	// Recorded in week 11.
	if got := ta.app.ListItems("")[0].Week; got != 11 {
		t.Fatalf("Week = %d, want 11", got)
	}
	// A folder whose audio is gone has no recording time; its name has a week.
	folder := filepath.Join(ta.dir, "알고리즘 9주차")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.store.Update(item.ID, func(it *store.Item) { it.Kind, it.Source = "folder", folder }); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].Week; got != 9 {
		t.Fatalf("Week = %d, want 9", got)
	}
	// A new semester start counts again.
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.store.Update(item.ID, func(it *store.Item) { it.Kind, it.Source = "file", filepath.Join(ta.dir, "녹음 23.m4a") }); err != nil {
		t.Fatal(err)
	}
	ta.setCourses(t, "2026-09-07", "선형대수학")
	if got := ta.app.ListItems("")[0].Week; got != 10 {
		t.Fatalf("Week after a new start = %d, want 10", got)
	}
}

func TestListGroupIsRemembered(t *testing.T) {
	ta := newTestApp(t)
	if g := ta.app.ListGroup(); g != "date" {
		t.Fatalf("default group = %q", g)
	}
	if err := ta.app.SetListGroup("course"); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.SetListGroup("bogus"); err == nil {
		t.Error("unknown group was accepted")
	}
	if g := ta.app.ListGroup(); g != "course" {
		t.Fatalf("group = %q", g)
	}
}

func TestSetWeekOverridesTheCountedWeek(t *testing.T) {
	ta, item := sortFixture(t, "부분공간")
	got := ta.app.ListItems("")[0]
	if got.Week != 11 || got.Recorded.IsZero() {
		t.Fatalf("counted Week = %d, Recorded = %v; want 11 and a time", got.Week, got.Recorded)
	}
	if err := ta.app.SetWeek(item.ID, 3); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].Week; got != 3 {
		t.Fatalf("Week after SetWeek = %d, want 3", got)
	}
	for _, bad := range []int{-1, MaxWeek + 1} {
		if err := ta.app.SetWeek(item.ID, bad); err == nil {
			t.Errorf("SetWeek(%d) accepted", bad)
		}
	}
	if err := ta.app.SetWeek("missing", 2); err == nil {
		t.Error("SetWeek on an unknown item accepted")
	}
	// 0 goes back to the counted week.
	if err := ta.app.SetWeek(item.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].Week; got != 11 {
		t.Fatalf("Week after reset = %d, want 11", got)
	}
}

func TestSetCoursesKeepsSlots(t *testing.T) {
	ta := newTestApp(t)
	slots := []config.Slot{{Day: 4, Start: "9:00", End: "10:15"}, {Day: 2, Start: "09:00", End: "10:15"}, {Day: 2, Start: "09:00", End: "10:15"}}
	if err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{{Name: "선형대수학", Slots: slots}}}); err != nil {
		t.Fatal(err)
	}
	got := ta.app.Courses().Courses[0].Slots
	want := []config.Slot{{Day: 2, Start: "09:00", End: "10:15"}, {Day: 4, Start: "09:00", End: "10:15"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Slots = %+v", got)
	}
	for _, bad := range []config.Slot{{Day: 8, Start: "09:00", End: "10:00"}, {Day: 1, Start: "10:00", End: "09:00"}, {Day: 1, Start: "9시", End: "10:00"}} {
		if err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{{Name: "a", Slots: []config.Slot{bad}}}}); err == nil {
			t.Errorf("bad slot %+v accepted", bad)
		}
	}
}

// A recording made during a class on the timetable goes to that class even
// without asking the classifier; it only names the topic, and is shown only
// that class.
func TestSortUsesTheTimetable(t *testing.T) {
	ta, item := sortFixture(t, "오늘은 출석만 부르고 끝") // Tuesday 13:00
	if err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{
		{Name: "선형대수학", Slots: []config.Slot{{Day: 2, Start: "10:30", End: "11:45"}}},
		{Name: "알고리즘", Slots: []config.Slot{{Day: 2, Start: "12:00", End: "13:15"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	var prompts, systems []string
	ta.app.NewClient = func(string) (gemini.Client, error) {
		return &promptRecorder{prompts: &prompts, systems: &systems, answer: `{"topic":"정렬 알고리즘"}`}, nil
	}
	sum, err := ta.app.SortCourses()
	if err != nil || sum.Matched != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	got, _ := ta.app.store.Get(item.ID)
	want := filepath.Join(ta.app.OutputDir, "알고리즘", course.FileBase(11, "정렬 알고리즘")+".md")
	if got.Course != "알고리즘" || got.Output != want {
		t.Fatalf("item = %q %q", got.Course, got.Output)
	}
	if len(prompts) != 1 || strings.Contains(prompts[0], "선형대수학") {
		t.Errorf("classifier saw other courses: %q", prompts)
	}
	if len(systems) != 1 || strings.Contains(systems[0], "confident") {
		t.Errorf("timed recording was asked for its course: %q", systems)
	}
}

type promptRecorder struct {
	gemini.Client
	prompts *[]string
	systems *[]string
	answer  string
}

func (p *promptRecorder) Generate(_ context.Context, req gemini.Request) (gemini.Result, error) {
	*p.prompts = append(*p.prompts, req.Parts[0].Text)
	if p.systems != nil {
		*p.systems = append(*p.systems, req.System)
	}
	return gemini.Result{Text: p.answer}, nil
}

func TestAddClassTimes(t *testing.T) {
	ta := newTestApp(t)
	got, err := ta.app.AddClassTimes("프로그래밍 언어론, 화 1100-1300")
	if err != nil || !got.New || got.Name != "프로그래밍 언어론" || len(got.Slots) != 1 {
		t.Fatalf("first add = %+v, %v", got, err)
	}
	// Same lecture, written without the space: the time joins it.
	got, err = ta.app.AddClassTimes("프로그래밍언어론 목12001300")
	if err != nil || got.New || got.Name != "프로그래밍 언어론" {
		t.Fatalf("second add = %+v, %v", got, err)
	}
	courses := ta.app.Courses().Courses
	if len(courses) != 1 || len(courses[0].Slots) != 2 {
		t.Fatalf("Courses = %+v", courses)
	}
	if _, err := ta.app.AddClassTimes("프로그래밍 언어론 화 11:00-13:00"); err == nil {
		t.Error("a time already there was added again")
	}
	// Overlaps are refused, across lectures and within one; back-to-back
	// classes are fine.
	if _, err := ta.app.AddClassTimes("논리회로 화 12:00-14:00"); err == nil || !strings.Contains(err.Error(), "프로그래밍 언어론") {
		t.Errorf("overlap across lectures: %v", err)
	}
	if _, err := ta.app.AddClassTimes("프로그래밍 언어론 화 12:30-13:30"); err == nil {
		t.Error("overlap within a lecture accepted")
	}
	if _, err := ta.app.AddClassTimes("논리회로 화 13:00-15:00"); err != nil {
		t.Errorf("back-to-back class refused: %v", err)
	}
	if _, err := ta.app.AddClassTimes("논리회로"); err == nil {
		t.Error("a line without times accepted")
	}
}

// SetCourses refuses an overlap. One written into the config by hand is
// dropped on reading, both times of it, and the lectures stay.
func TestCoursesOverlaps(t *testing.T) {
	ta := newTestApp(t)
	slot := func(day int, start, end string) []config.Slot {
		return []config.Slot{{Day: day, Start: start, End: end}}
	}
	if err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{
		{Name: "a", Slots: slot(1, "09:00", "11:00")},
		{Name: "b", Slots: slot(1, "10:00", "12:00")},
	}}); err == nil {
		t.Fatal("overlap accepted")
	}
	// Nothing was saved, so the config is written from scratch.
	raw := map[string]json.RawMessage{}
	raw["courses"] = json.RawMessage(`[{"name":"a","slots":[{"day":1,"start":"09:00","end":"11:00"},{"day":3,"start":"09:00","end":"11:00"}]},{"name":"b","slots":[{"day":1,"start":"10:00","end":"12:00"}]}]`)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ta.cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(ta.cfgPath) // as the app reads it at start
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Courses()
	if len(got) != 2 || len(got[0].Slots) != 1 || got[0].Slots[0].Day != 3 || len(got[1].Slots) != 0 {
		t.Fatalf("Courses = %+v", got)
	}
	// What is read back saves without complaint.
	if err := cfg.SetCourses(got, ""); err != nil {
		t.Errorf("saving the cleaned list: %v", err)
	}
}

// With the audio deleted and no kept time, the week comes from the names.
func TestWeekOfWithoutAudio(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	cases := []struct {
		item store.Item
		want int
	}{
		{store.Item{Kind: "file", Source: filepath.Join(gone, norm.NFD.String("컴퓨터구조 13주차-2.m4a")), Name: "x"}, 13},
		{store.Item{Kind: "folder", Source: filepath.Join(gone, norm.NFD.String("9주차")), Name: "9주차"}, 9},
		{store.Item{Kind: "file", Source: filepath.Join(gone, "Chung-Ang University 11.m4a"), Name: "Week 12 - Direct-mapped cache"}, 12},
		{store.Item{Kind: "file", Source: filepath.Join(gone, "흑석동 8.m4a"), Name: "eigenvalue와 basis 변환"}, 0},
	}
	for _, c := range cases {
		if week, _ := weekOf(c.item, ""); week != c.want {
			t.Errorf("weekOf(%q, %q) = %d, want %d", c.item.Source, c.item.Name, week, c.want)
		}
	}
}

// A transcript outside the transcript folder (another drive, a guarded
// folder) is written anew into the lecture folder; the old file stays.
func TestSortCopiesTranscriptFromOutside(t *testing.T) {
	ta, item := sortFixture(t, "부분공간 span basis")
	ta.setCourses(t, "", "선형대수학")
	if err := ta.app.AddKey("", "free", "AIza"+strings.Repeat("f", 35)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(ta.dir, "D", "녹음 23.md")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(item.Output, outside); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.store.Update(item.ID, func(it *store.Item) { it.Output = outside }); err != nil {
		t.Fatal(err)
	}
	// The lecture folder already holds that name.
	taken := filepath.Join(ta.app.OutputDir, "선형대수학", course.FileBase(11, "부분공간")+".md")
	if err := os.MkdirAll(filepath.Dir(taken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taken, []byte("다른 강의록\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ta.withSorter(t, map[string]string{"부분공간": `{"course":"선형대수학","confident":true,"topic":"부분공간"}`}, nil)
	if sum, err := ta.app.SortCourses(); err != nil || sum.Matched != 1 {
		t.Fatalf("SortCourses = %+v, %v", sum, err)
	}
	got, _ := ta.app.store.Get(item.ID)
	want := filepath.Join(ta.app.OutputDir, "선형대수학", course.FileBase(11, "부분공간")+" (2).md")
	if got.Output != want {
		t.Fatalf("Output = %q, want %q", got.Output, want)
	}
	if data, _ := os.ReadFile(want); string(data) != "부분공간 span basis" {
		t.Fatalf("copy = %q", data)
	}
	if data, _ := os.ReadFile(taken); string(data) != "다른 강의록\n" {
		t.Fatal("a file already in the lecture folder was replaced")
	}
	if !isFile(outside) {
		t.Fatal("the transcript outside the folder must stay")
	}
}
