package macdrop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewDropDirMakesFreshFolders(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 3, 14, 5, 9, 0, time.Local)
	a, err := newDropDir(root, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newDropDir(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("same folder for two drops: %s", a)
	}
	for _, dir := range []string{a, b} {
		if filepath.Dir(dir) != filepath.Join(root, "dropped") {
			t.Errorf("%s is not under <work>/dropped", dir)
		}
		if !strings.HasPrefix(filepath.Base(dir), "20261103-140509-") {
			t.Errorf("%s does not start with the drop time", dir)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s was not made: %v", dir, err)
		}
	}
}

func TestCollectorBatchesOneDrop(t *testing.T) {
	got := make(chan []string, 4)
	c := newCollector(40*time.Millisecond, func(paths []string) { got <- paths })

	c.add("/w/dropped/x/New Recording 12.m4a")
	c.add("")
	c.add("/w/dropped/x/New Recording 13.m4a")

	select {
	case paths := <-got:
		want := []string{"/w/dropped/x/New Recording 12.m4a", "/w/dropped/x/New Recording 13.m4a"}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("batch = %q, want %q", paths, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no batch handed on")
	}

	// A later drop is a new batch.
	c.add("/w/dropped/y/a.m4a")
	select {
	case paths := <-got:
		if !reflect.DeepEqual(paths, []string{"/w/dropped/y/a.m4a"}) {
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
