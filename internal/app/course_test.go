package app

import (
	"context"
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
	out := filepath.Join(ta.dir, "녹음 23.md")
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
	want := filepath.Join(ta.dir, "선형대수학", base+".md")
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
	if want := filepath.Join(ta.dir, "알고리즘", course.FileBase(10, "정렬")+".md"); again.Output != want {
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
	err := ta.app.SetCourses(CourseSettings{Courses: []config.Course{{Name: " 선형대수학 "}, {Name: "선형대수학"}, {Name: ""}, {Name: "알고리즘", Hint: "김교수"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := ta.app.Courses()
	if len(got.Courses) != 2 || got.Courses[0].Name != "선형대수학" || got.Courses[1].Hint != "김교수" {
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
	if want := filepath.Join(ta.dir, "프로그래밍언어론", course.FileBase(11, "자료형")+".md"); got.Output != want {
		t.Fatalf("output = %s, want %s", got.Output, want)
	}
}

func TestSortFolderGoesNextToTheFolder(t *testing.T) {
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
	want := filepath.Join(ta.dir, "선형대수학", course.FileBase(10, "부분공간")+".md")
	for _, id := range []string{"i1", "i2"} {
		got, _ := ta.app.store.Get(id)
		if got.Output != want || got.Sorted != sortedMatched || got.Course != "선형대수학" {
			t.Errorf("%s = %q %q", id, got.Output, got.Sorted)
		}
	}
	if !isFile(want) {
		t.Error("transcript was not moved next to the folder")
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
