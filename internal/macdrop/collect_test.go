package macdrop

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDirIsUnderDocuments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Documents", "Nalmeok", "음성 메모"); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
}

// receive stands in for the source writing a promised file.
func receive(t *testing.T, dest, name, body string) string {
	t.Helper()
	stage, err := newStageDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(stage), ".") || filepath.Dir(stage) != dest {
		t.Fatalf("stage folder %s is not hidden inside %s", stage, dest)
	}
	staged := filepath.Join(stage, name)
	if err := os.WriteFile(staged, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	final, err := place(staged, dest)
	if err != nil {
		t.Fatal(err)
	}
	removeStageDir(stage)
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("stage folder %s left behind", stage)
	}
	return final
}

func TestPlaceKeepsNameAndNumbersClashes(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "Nalmeok", "voice")
	name := "New Recording 12.m4a"

	first := receive(t, dest, name, "one")
	second := receive(t, dest, name, "two")
	third := receive(t, dest, name, "three")

	want := []string{
		filepath.Join(dest, "New Recording 12.m4a"),
		filepath.Join(dest, "New Recording 12 (2).m4a"),
		filepath.Join(dest, "New Recording 12 (3).m4a"),
	}
	if got := []string{first, second, third}; !reflect.DeepEqual(got, want) {
		t.Fatalf("placed at %q, want %q", got, want)
	}
	for i, body := range []string{"one", "two", "three"} {
		data, err := os.ReadFile(want[i])
		if err != nil || string(data) != body {
			t.Errorf("%s = %q, %v; want %q", want[i], data, err, body)
		}
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 3 {
		t.Errorf("destination holds %d entries, want the 3 recordings", len(entries))
	}
}

func TestPlaceConcurrentReceivesGetDistinctNames(t *testing.T) {
	dest := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	finals := make([]string, n)
	for i := 0; i < n; i++ {
		stage, err := newStageDir(dest)
		if err != nil {
			t.Fatal(err)
		}
		staged := filepath.Join(stage, "a.m4a")
		if err := os.WriteFile(staged, []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(i int, staged string) {
			defer wg.Done()
			final, err := place(staged, dest)
			if err != nil {
				t.Error(err)
			}
			finals[i] = final
		}(i, staged)
	}
	wg.Wait()
	seen := map[string]bool{}
	bodies := []string{}
	for _, f := range finals {
		if seen[f] {
			t.Fatalf("two receives placed at %s", f)
		}
		seen[f] = true
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(data))
	}
	sort.Strings(bodies)
	if len(bodies) != n || len(seen) != n {
		t.Fatalf("got %d files", len(seen))
	}
}

func TestRemoveStageDirLeavesOtherFolders(t *testing.T) {
	dest := t.TempDir()
	other := filepath.Join(dest, "keep")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	removeStageDir(other)
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("removed a folder that is not a stage folder: %v", err)
	}
}

func TestCollectorBatchesOneDrop(t *testing.T) {
	got := make(chan []string, 4)
	c := newCollector(40*time.Millisecond, func(paths []string) { got <- paths })

	c.add("/d/New Recording 12.m4a")
	c.add("")
	c.add("/d/New Recording 13.m4a")

	select {
	case paths := <-got:
		want := []string{"/d/New Recording 12.m4a", "/d/New Recording 13.m4a"}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("batch = %q, want %q", paths, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no batch handed on")
	}

	// A later drop is a new batch.
	c.add("/d/a.m4a")
	select {
	case paths := <-got:
		if !reflect.DeepEqual(paths, []string{"/d/a.m4a"}) {
			t.Fatalf("second batch = %q", paths)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no second batch")
	}
	select {
	case extra := <-got:
		t.Fatalf("unexpected batch %q", extra)
	case <-time.After(100 * time.Millisecond):
	}
}
