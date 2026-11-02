package output

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two items finishing for the same source at once must not overwrite each
// other: each gets its own numbered file.
func TestWriteNewConcurrentSameTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "강의.md")
	const n = 8
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			paths[i], errs[i] = WriteNew(target, fmt.Sprintf("text %d\n", i))
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("WriteNew %d: %v", i, errs[i])
		}
		if seen[paths[i]] {
			t.Fatalf("two writers got the same path %s", paths[i])
		}
		seen[paths[i]] = true
		if got := readText(t, paths[i]); got != fmt.Sprintf("text %d\n", i) {
			t.Errorf("%s = %q, want text %d", paths[i], got, i)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Errorf("dir has %d entries, want %d (temp files left?)", len(entries), n)
	}
}

func TestWriteNewKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.md")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := WriteNew(target, "new\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "a (2).md"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if readText(t, target) != "old" {
		t.Fatal("existing file was overwritten")
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
