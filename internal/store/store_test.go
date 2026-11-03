package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "items.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func TestAddListNewestFirstAndSearch(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	items := []Item{
		{ID: "old", Name: "강의 2강.m4a", Source: "/lectures/ch07/강의 2강.m4a", Kind: "file", State: "done", CreatedAt: base},
		{ID: "new", Name: "ch07", Source: "/lectures/CH07", Kind: "folder", State: "failed", CreatedAt: base.Add(time.Minute)},
		{ID: "mid", Name: "Other.mp3", Source: "/music/Other.mp3", Kind: "file", State: "done", CreatedAt: base.Add(30 * time.Second)},
	}
	for _, item := range items {
		if err := s.Add(item); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	got := s.List("")
	if len(got) != 3 {
		t.Fatalf("List = %d, want 3", len(got))
	}
	wantOrder := []string{"new", "mid", "old"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Fatalf("List[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}

	if got := s.List("강의"); len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("Korean name search = %+v", got)
	}
	if got := s.List("ch07"); len(got) != 2 {
		t.Fatalf("case-insensitive source search = %+v, want 2", got)
	}
	if got := s.List("  CH07  "); len(got) != 2 {
		t.Fatalf("trimmed search = %+v, want 2", got)
	}
	if got := s.List("없는이름"); len(got) != 0 {
		t.Fatalf("no-match search = %+v, want 0", got)
	}
}

func TestUpdateGetRemovePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Item{ID: "a", Name: "강의.m4a", Source: "/x/강의.m4a", Kind: "file", State: "running"}); err != nil {
		t.Fatal(err)
	}

	before, ok := s.Get("a")
	if !ok {
		t.Fatal("Get did not find item")
	}
	if err := s.Update("a", func(item *Item) {
		item.State = "done"
		item.Progress = 1
		item.Output = "/x/강의.md"
		item.Units = []Unit{{Index: 0, Files: []string{"/tmp/a.aac"}, MIMEs: []string{"audio/m4a"}, Text: "본문"}}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, ok := s.Get("a")
	if !ok {
		t.Fatal("Get after update failed")
	}
	if after.State != "done" || after.Progress != 1 || after.Output != "/x/강의.md" {
		t.Fatalf("updated item = %+v", after)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) && !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance")
	}

	// Returned copies must not mutate the store.
	after.Units[0].Files[0] = "/changed"
	after.Units[0].MIMEs[0] = "audio/changed"
	if again, _ := s.Get("a"); again.Units[0].Files[0] != "/tmp/a.aac" || again.Units[0].MIMEs[0] != "audio/m4a" {
		t.Fatalf("Get leaked internal slice: %+v", again.Units)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, ok := reloaded.Get("a"); !ok || again.Output != "/x/강의.md" {
		t.Fatalf("persisted item = %+v, ok=%v", again, ok)
	}

	if err := s.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("item still present after Remove")
	}
	if err := s.Remove("a"); err != ErrNotFound {
		t.Fatalf("second Remove = %v, want ErrNotFound", err)
	}
	if err := s.Update("a", func(*Item) {}); err != ErrNotFound {
		t.Fatalf("Update missing = %v, want ErrNotFound", err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("perm = %o, want 600", perm)
		}
	}
}

func TestOnChangeFiresAfterMutations(t *testing.T) {
	s := newTestStore(t)
	var count atomic.Int64
	s.SetOnChange(func() { count.Add(1) })

	if err := s.Add(Item{ID: "a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update("a", func(item *Item) { item.State = "done" }); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("callback count = %d, want 3", got)
	}
	if err := s.Update("missing", func(*Item) {}); err != ErrNotFound {
		t.Fatalf("missing update = %v", err)
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("callback fired for a failed mutation (count %d)", got)
	}
}

func TestUpdateWithoutChangeSkipsSaveAndNotify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Item{ID: "a", Name: "a", State: "running", Detail: "queued"}); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	s.SetOnChange(func() { count.Add(1) })
	before, _ := s.Get("a")

	// A marker the store would overwrite if it saved. Proving it is still
	// there after the call proves no write happened.
	if err := os.WriteFile(path, []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Update("a", func(*Item) {}); err != nil {
		t.Fatalf("no-op Update: %v", err)
	}
	if got := count.Load(); got != 0 {
		t.Fatalf("onChange fired %d times for a no-op update", got)
	}
	after, _ := s.Get("a")
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("no-op Update changed UpdatedAt: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "marker" {
		t.Fatalf("no-op Update wrote the file: %q, %v", data, err)
	}

	// A real change is saved and notifies once.
	if err := s.Update("a", func(item *Item) { item.State = "done" }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("onChange count = %d, want 1", got)
	}
	if after, _ := s.Get("a"); !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatal("a changing Update did not advance UpdatedAt")
	}
}

func TestUpdateDetectsBatchUploadsChange(t *testing.T) {
	s := newTestStore(t)
	if err := s.Add(Item{ID: "b", Batch: &BatchRef{
		KeyID:   "k",
		Uploads: map[string]Upload{"a.m4a": {URI: "old", MIME: "audio/m4a"}},
	}}); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	s.SetOnChange(func() { count.Add(1) })

	if err := s.Update("b", func(item *Item) {
		item.Batch.Uploads["a.m4a"] = Upload{URI: "new", MIME: "audio/m4a"}
	}); err != nil {
		t.Fatal(err)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("onChange count = %d, want 1 for a changed upload entry", got)
	}
	got, _ := s.Get("b")
	if up := got.Batch.Uploads["a.m4a"]; up.URI != "new" {
		t.Fatalf("upload = %+v, want URI new", up)
	}
}

func TestAddManySavesAndNotifiesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	s.SetOnChange(func() { count.Add(1) })

	items := make([]Item, 10)
	for i := range items {
		items[i] = Item{Name: fmt.Sprintf("lec%02d.m4a", i), Source: fmt.Sprintf("/x/lec%02d.m4a", i), Kind: "file", State: "running"}
	}
	if err := s.AddMany(items); err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("onChange count = %d, want 1", got)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.List("")
	if len(got) != 10 {
		t.Fatalf("reloaded items = %d, want 10", len(got))
	}
	for _, item := range got {
		if item.ID == "" || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
			t.Fatalf("AddMany left ID/timestamps empty: %+v", item)
		}
	}
}

func TestAddManyRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Item{ID: "keep", Name: "keep"}); err != nil {
		t.Fatal(err)
	}
	// Replace the file with a non-empty directory so the atomic rename fails.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "block"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := s.AddMany([]Item{{ID: "x", Name: "x"}, {ID: "y", Name: "y"}}); err == nil {
		t.Fatal("AddMany succeeded with an unwritable path")
	}
	got := s.List("")
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("items after a failed AddMany = %+v, want only keep", got)
	}
}

func TestConcurrentAdds(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Add(Item{Name: "동시"}); err != nil {
				t.Errorf("concurrent Add: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(s.List("")); got != 20 {
		t.Fatalf("items = %d, want 20", got)
	}
	for _, item := range s.List("") {
		if item.ID == "" {
			t.Fatal("Add did not assign an ID")
		}
	}
}

func TestLoadMissingAndCorruptStartEmpty(t *testing.T) {
	dir := t.TempDir()

	missing, err := Load(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("missing Load: %v", err)
	}
	if got := len(missing.List("")); got != 0 {
		t.Fatalf("missing items = %d", got)
	}

	corruptPath := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt, err := Load(corruptPath)
	if err != nil {
		t.Fatalf("corrupt Load: %v", err)
	}
	if got := len(corrupt.List("")); got != 0 {
		t.Fatalf("corrupt items = %d", got)
	}
}

func TestNewIDIsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
}
