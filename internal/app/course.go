package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/course"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/media"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/store"
)

// Item sorting states (store.Item.Sorted).
const (
	sortedMatched = "matched"
	sortedNone    = "none"
)

// CourseSettings is the lecture list as the settings sheet edits it.
type CourseSettings struct {
	Courses []config.Course `json:"Courses"`
	// SemesterStart is "2026-09-01", or "" to use the first weekday of
	// March or September.
	SemesterStart string `json:"SemesterStart"`
}

// SortSummary says what one "sort into lectures" run did.
type SortSummary struct {
	Matched int `json:"Matched"`
	None    int `json:"None"`
	Failed  int `json:"Failed"`
	// Error is the first failure's message, for the toast.
	Error string `json:"Error,omitempty"`
}

// Courses returns the lecture list and semester start.
func (a *App) Courses() CourseSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return CourseSettings{Courses: []config.Course{}}
	}
	list := a.cfg.Courses()
	if list == nil {
		list = []config.Course{}
	}
	return CourseSettings{Courses: list, SemesterStart: a.cfg.SemesterStart()}
}

// SetCourses saves the lecture list and semester start.
func (a *App) SetCourses(settings CourseSettings) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	return a.cfg.SetCourses(settings.Courses, settings.SemesterStart)
}

// ClassTimesAdded says what AddClassTimes did, for the toast.
type ClassTimesAdded struct {
	Settings CourseSettings `json:"Settings"`
	Name     string         `json:"Name"`  // the lecture, as saved
	Slots    []config.Slot  `json:"Slots"` // the times added
	New      bool           `json:"New"`   // the lecture was added too
}

// AddClassTimes reads a typed line ("프로그래밍, 화 1500~1900") and adds its
// class times to the lecture of that name, or to a new lecture. Names match
// without regard to case or spaces, so "프로그래밍언어론" adds to "프로그래밍
// 언어론". Times already there are skipped; a time that overlaps another
// class is refused.
func (a *App) AddClassTimes(text string) (ClassTimesAdded, error) {
	entry, err := course.ParseEntry(text)
	if err != nil {
		return ClassTimesAdded{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ClassTimesAdded{}, errNoConfig
	}
	list := a.cfg.Courses()
	at := -1
	for i, c := range list {
		if sameCourseName(c.Name, entry.Name) {
			at = i
			break
		}
	}
	added := ClassTimesAdded{Name: entry.Name, New: at < 0}
	if at < 0 {
		list = append(list, config.Course{Name: entry.Name})
		at = len(list) - 1
	}
	added.Name = list[at].Name
	for _, slot := range entry.Slots {
		if !slices.Contains(list[at].Slots, slot) {
			list[at].Slots = append(list[at].Slots, slot)
			added.Slots = append(added.Slots, slot)
		}
	}
	if len(added.Slots) == 0 {
		return ClassTimesAdded{}, i18n.Error("error.entryExists", "name", added.Name)
	}
	if err := a.cfg.SetCourses(list, a.cfg.SemesterStart()); err != nil {
		return ClassTimesAdded{}, err
	}
	added.Settings = CourseSettings{Courses: a.cfg.Courses(), SemesterStart: a.cfg.SemesterStart()}
	return added, nil
}

// sameCourseName: equal ignoring case and spaces.
func sameCourseName(a, b string) bool {
	flat := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	return flat(a) == flat(b)
}

// SortableCount is how many finished items "sort into lectures" would look
// at: finished, never sorted, with a transcript to read.
func (a *App) SortableCount() int {
	return len(a.sortable())
}

func (a *App) sortable() []store.Item {
	if a.store == nil {
		return nil
	}
	var out []store.Item
	for _, item := range a.store.List("") {
		// A transcript that is gone (its folder deleted, or moved by a
		// duplicate item's sort) has nothing to sort.
		if item.State == "done" && item.Sorted == "" && isFile(item.Output) {
			out = append(out, item)
		}
	}
	return out
}

// SortCourses sorts every finished item that was never sorted into the
// lecture list: the .md moves to "<transcript folder>/<lecture>/<n>주차-<topic>.md".
// It runs only when asked. Items that fit no lecture stay where they are and
// are marked, so the next run skips them; SortCourse redoes one.
func (a *App) SortCourses() (SortSummary, error) {
	return a.sortItems(a.sortable())
}

// SortCourse sorts one finished item again, whatever it was sorted as.
func (a *App) SortCourse(id string) (SortSummary, error) {
	item, err := a.finishedItem(id)
	if err != nil {
		return SortSummary{}, err
	}
	return a.sortItems([]store.Item{item})
}

func (a *App) sortItems(items []store.Item) (SortSummary, error) {
	a.mu.Lock()
	var courses []config.Course
	var start string
	var keys []config.Key
	if a.cfg != nil {
		courses, start, keys = a.cfg.Courses(), a.cfg.SemesterStart(), a.cfg.AllKeys()
	}
	a.mu.Unlock()
	if len(courses) == 0 {
		return SortSummary{}, i18n.Error("error.noCourses")
	}
	if len(keys) == 0 {
		return SortSummary{}, i18n.Error("error.keyRequired")
	}
	ctx := a.context()
	if ctx == nil {
		ctx = context.Background()
	}
	clients := &classifierKeys{keys: freeFirst(keys), newClient: a.newClient()}
	var sum SortSummary
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		// A duplicate sorted earlier in this run moved this transcript too.
		if now, ok := a.store.Get(item.ID); !ok || now.Output != item.Output {
			continue
		}
		matched, err := a.sortOne(ctx, clients, item, courses, start)
		switch {
		case err != nil:
			sum.Failed++
			if sum.Error == "" {
				sum.Error = errorMessage(err)
			}
			if clients.exhausted() {
				return sum, nil // every key is limited or rejected: stop asking
			}
		case matched:
			sum.Matched++
		default:
			sum.None++
		}
	}
	return sum, nil
}

// sortOne classifies one item and moves its .md. matched is false when no
// lecture fit; the item is then only marked.
func (a *App) sortOne(ctx context.Context, clients *classifierKeys, item store.Item, courses []config.Course, start string) (bool, error) {
	text, err := a.outputText(item)
	if err != nil {
		return false, err
	}
	name := recordingName(item)
	week, _ := a.weeks.of(item, start)
	// A recording made during a class on the timetable is that class (the
	// one it shares the most time with): the model then only names the
	// topic, with a prompt that does not ask for the course.
	var slot config.Course
	var timed bool
	if from, to, ok := recordedSpan(item); ok {
		slot, timed = course.ByTime(courses, from, to)
	}
	res, err := clients.ask(func(client gemini.Client) (course.Result, error) {
		if !timed {
			return course.Classify(ctx, client, text, name, courses)
		}
		topic, err := course.Topic(ctx, client, text, name, slot)
		return course.Result{Course: slot.Name, Topic: topic}, err
	})
	if err != nil {
		return false, err
	}
	if res.Course == "" {
		return false, a.updateTranscript(item, func(it *store.Item) { it.Sorted = sortedNone })
	}
	if !isFile(item.Output) {
		return false, errNoOutput
	}
	base := course.FileBase(week, res.Topic)
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(item.Output), ".md")
	}
	if a.OutputDir == "" {
		return false, errNotReady
	}
	dir := filepath.Join(a.OutputDir, res.Course)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, i18n.Wrap("error.renameFailed", err)
	}
	target := filepath.Join(dir, base+".md")
	if target != item.Output {
		if target, err = placeSorted(a.OutputDir, item.Output, target, text); err != nil {
			return false, i18n.Wrap("error.renameFailed", err)
		}
	}
	return true, a.updateTranscript(item, func(it *store.Item) {
		it.Output = target
		it.Name = strings.TrimSuffix(filepath.Base(target), ".md")
		it.Course = res.Course
		it.Sorted = sortedMatched
	})
}

// placeSorted puts a sorted transcript at target, or a numbered name when
// that is taken, and returns where it went. The name is claimed first
// (output.Reserve), so the startup copy writing the same name meanwhile is
// never replaced. A .md in the transcript folder moves. One outside it (an
// older result the startup copy has not taken in, a file picked with
// "locate") is written anew from text and left where it is: it may sit on
// another drive, where a rename fails, or in a folder macOS guards.
func placeSorted(root, from, target, text string) (string, error) {
	if !output.Inside(root, from) {
		return output.WriteNew(target, text)
	}
	claimed, err := output.Reserve(target)
	if err != nil {
		return "", err
	}
	if err := os.Rename(from, claimed); err != nil {
		_ = os.Remove(claimed)
		return "", err
	}
	return claimed, nil
}

// updateTranscript applies fn to item and to every other item writing to the
// same .md: the same recording added twice shares one transcript, and a copy
// left behind would point at a file that moved.
func (a *App) updateTranscript(item store.Item, fn func(*store.Item)) error {
	for _, other := range a.store.List("") {
		if other.ID != item.ID && other.Output != item.Output {
			continue
		}
		if err := a.store.Update(other.ID, fn); err != nil {
			return err
		}
	}
	return nil
}

// weekOf is the semester week item's recording belongs to, or 0, and when it
// was recorded (zero when that cannot be read). The week is counted from the
// recording time: kept on the item, else read from the audio. When neither
// is there (the audio was deleted before the time was kept), it is the week
// written in the recording's name ("9주차", "week 9"), else in the item's
// name, which sorting set to "<n>주차 - <topic>".
func weekOf(item store.Item, start string) (int, time.Time) {
	// The start, as sorting uses: a recording that runs past midnight on
	// Sunday belongs to the week it began in.
	if at, _, ok := recordedSpan(item); ok && !at.IsZero() {
		return course.Week(at, course.SemesterStart(at, start)), at
	}
	if week := course.WeekFromName(recordingName(item)); week > 0 {
		return week, time.Time{}
	}
	return course.WeekFromName(item.Name), time.Time{}
}

// weekCache remembers weekOf per item, so a list refresh does not read the
// audio of an item whose recording time is not kept yet. An entry holds
// while everything weekOf reads from the item, and the semester start, are
// the same.
type weekCache struct {
	mu sync.Mutex
	m  map[string]weekEntry
}

type weekEntry struct {
	source, name, start string
	kept                time.Time // item.RecordedFrom when counted
	week                int
	recorded            time.Time
}

// of is item's week and recording time. A week the user set (WeekSet) wins
// over the counted one.
func (c *weekCache) of(item store.Item, start string) (int, time.Time) {
	c.mu.Lock()
	e, ok := c.m[item.ID]
	c.mu.Unlock()
	if !ok || e.source != item.Source || e.name != item.Name || !e.kept.Equal(item.RecordedFrom) || e.start != start {
		week, at := weekOf(item, start)
		e = weekEntry{source: item.Source, name: item.Name, start: start, kept: item.RecordedFrom, week: week, recorded: at}
		c.mu.Lock()
		if c.m == nil {
			c.m = make(map[string]weekEntry)
		}
		c.m[item.ID] = e
		c.mu.Unlock()
	}
	if item.WeekSet > 0 {
		return item.WeekSet, e.recorded
	}
	return e.week, e.recorded
}

// MaxWeek is the highest week SetWeek takes.
const MaxWeek = 30

// SetWeek sets the semester week of item id by hand; 0 goes back to the
// counted week. Copies of the same transcript get it too. A sorted
// transcript keeps its file name; sorting it again names it with this week.
func (a *App) SetWeek(id string, week int) error {
	if week < 0 || week > MaxWeek {
		return i18n.Error("error.weekRange", "max", MaxWeek)
	}
	if a.store == nil {
		return store.ErrNotFound
	}
	item, ok := a.store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	return a.updateTranscript(item, func(it *store.Item) { it.WeekSet = week })
}

func (a *App) semesterStart() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ""
	}
	return a.cfg.SemesterStart()
}

// recordingName is the name the user gave the recording: a file's name
// without extension, or a folder's name as it is ("algorithm_09.1" keeps
// its ".1"). A new transcript gets the same name (output.TargetPath).
func recordingName(item store.Item) string {
	return output.Name(item.Source, item.Kind)
}

// recordedSpan is when item's recording ran: kept on the item, or read from
// the audio when an older item has none yet.
func recordedSpan(item store.Item) (time.Time, time.Time, bool) {
	if !item.RecordedFrom.IsZero() {
		return item.RecordedFrom, item.RecordedTo, true
	}
	return media.RecordedSpan(item.Source)
}

func (a *App) newClient() func(string) (gemini.Client, error) {
	if a.NewClient != nil {
		return a.NewClient
	}
	return gemini.New
}

// freeFirst orders keys free first, keeping each tier's priority: sorting is
// a tiny request the free tier covers.
func freeFirst(keys []config.Key) []config.Key {
	out := make([]config.Key, 0, len(keys))
	for _, tier := range []string{config.TierFree, config.TierPaid} {
		for _, k := range keys {
			if k.Tier == tier {
				out = append(out, k)
			}
		}
	}
	return out
}

// classifierKeys hands out clients, moving to the next key for good when one
// is limited (429) or rejected.
type classifierKeys struct {
	keys      []config.Key
	next      int
	client    gemini.Client
	newClient func(string) (gemini.Client, error)
}

func (c *classifierKeys) exhausted() bool { return c.next >= len(c.keys) }

// ask runs fn with the current key's client, moving on to the next key when
// one is limited or rejected.
func (c *classifierKeys) ask(fn func(gemini.Client) (course.Result, error)) (course.Result, error) {
	var last error
	for !c.exhausted() {
		if c.client == nil {
			client, err := c.newClient(c.keys[c.next].APIKey)
			if err != nil {
				last = err
				c.next++
				continue
			}
			c.client = client
		}
		res, err := fn(c.client)
		if err == nil {
			return res, nil
		}
		last = err
		if !gemini.Is429(err) && !gemini.IsAuth(err) {
			return course.Result{}, err
		}
		c.client = nil
		c.next++
	}
	if last == nil {
		last = i18n.Error("error.keyRequired")
	}
	return course.Result{}, last
}

// errorMessage is the short sentence a sorting failure shows.
func errorMessage(err error) string {
	switch {
	case gemini.IsAuth(err):
		return i18n.T("error.apiKeyInvalid")
	case gemini.Is429(err):
		return i18n.T("error.rateLimited")
	case gemini.IsTransient(err):
		return i18n.T("error.network")
	case i18n.IsText(err):
		return err.Error()
	}
	return i18n.T("error.failed")
}
