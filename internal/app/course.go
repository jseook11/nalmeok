package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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
		if item.State == "done" && item.Output != "" && item.Sorted == "" {
			out = append(out, item)
		}
	}
	return out
}

// SortCourses sorts every finished item that was never sorted into the
// lecture list: the .md moves to "<its folder>/<lecture>/<n>주차-<topic>.md".
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
	res, err := clients.classify(ctx, text, courses)
	if err != nil {
		return false, err
	}
	if res.Course == "" {
		return false, a.store.Update(item.ID, func(it *store.Item) { it.Sorted = sortedNone })
	}
	if !isFile(item.Output) {
		return false, errNoOutput
	}
	week := 0
	if at, ok := media.RecordedAt(item.Source); ok {
		week = course.Week(at, course.SemesterStart(at, start))
	}
	base := course.FileBase(week, res.Topic)
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(item.Output), ".md")
	}
	// A transcript sorted before sits in its old lecture's folder; the new
	// folder goes next to that one, not inside it.
	root := filepath.Dir(item.Output)
	if item.Course != "" && filepath.Base(root) == item.Course {
		root = filepath.Dir(root)
	}
	dir := filepath.Join(root, res.Course)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, i18n.Wrap("error.renameFailed", err)
	}
	target := filepath.Join(dir, base+".md")
	if target != item.Output {
		target = output.UniquePath(target)
		if err := os.Rename(item.Output, target); err != nil {
			return false, i18n.Wrap("error.renameFailed", err)
		}
	}
	return true, a.store.Update(item.ID, func(it *store.Item) {
		it.Output = target
		it.Name = strings.TrimSuffix(filepath.Base(target), ".md")
		it.Course = res.Course
		it.Sorted = sortedMatched
	})
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
	for _, tier := range []string{"free", "paid"} {
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

func (c *classifierKeys) classify(ctx context.Context, text string, courses []config.Course) (course.Result, error) {
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
		res, err := course.Classify(ctx, c.client, text, courses)
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
