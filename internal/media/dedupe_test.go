package media

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A lecture converted from .mp4 to .m4a keeps both files; only the .m4a is
// transcribed, like the Python app (old/nalmeok/media.py estimate_files).
func TestScanSkipsMP4WithSameStemM4A(t *testing.T) {
	fx := fixtures(t)
	dir := t.TempDir()
	link(t, filepath.Join(fx, "x.mp4"), filepath.Join(dir, "1강.mp4"))
	link(t, filepath.Join(fx, "x.m4a"), filepath.Join(dir, "1강.M4A"))
	link(t, filepath.Join(fx, "x.mp4"), filepath.Join(dir, "2강.mp4"))
	in, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tr := range in.Tracks {
		names = append(names, filepath.Base(tr.Path))
	}
	if !slices.Equal(names, []string{"1강.M4A", "2강.mp4"}) {
		t.Errorf("tracks = %v, want the .m4a for 1강 and the lone 2강.mp4", names)
	}
}

// Needs no fixtures: the pair must be collapsed before the file count check.
func TestScanCountsConvertedPairsOnce(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 10; i++ {
		touch(t, dir, fmt.Sprintf("%d.mp4", i), fmt.Sprintf("%d.m4a", i))
	}
	if _, err := Scan(dir); err != nil && strings.Contains(err.Error(), "최대") {
		t.Fatalf("10 lectures with .mp4 and .m4a counted twice: %v", err)
	}
}
