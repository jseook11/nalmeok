package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A file without a creation time ends when it was last written; its span
// reaches back by its length. A folder spans its first start to last end.
func TestRecordedSpan(t *testing.T) {
	dir := t.TempDir()
	stop := time.Date(2026, 11, 10, 15, 2, 0, 0, time.Local)
	one := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(one, pcmWAVBytes(120), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(one, stop, stop); err != nil {
		t.Fatal(err)
	}
	start, end, ok := RecordedSpan(one)
	if !ok || !end.Equal(stop) || !start.Equal(stop.Add(-2*time.Minute)) {
		t.Fatalf("file span = %v – %v, %v", start, end, ok)
	}

	two := filepath.Join(dir, "b.wav")
	if err := os.WriteFile(two, pcmWAVBytes(60), 0o600); err != nil {
		t.Fatal(err)
	}
	later := stop.Add(10 * time.Minute)
	if err := os.Chtimes(two, later, later); err != nil {
		t.Fatal(err)
	}
	start, end, ok = RecordedSpan(dir)
	if !ok || !start.Equal(stop.Add(-2*time.Minute)) || !end.Equal(later) {
		t.Fatalf("folder span = %v – %v, %v", start, end, ok)
	}

	// Unreadable length: one moment, as fileRecordedAt.
	bad := filepath.Join(t.TempDir(), "c.m4a")
	if err := os.WriteFile(bad, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bad, stop, stop); err != nil {
		t.Fatal(err)
	}
	start, end, ok = RecordedSpan(bad)
	if !ok || !start.Equal(stop) || !end.Equal(stop) {
		t.Fatalf("unreadable span = %v – %v, %v", start, end, ok)
	}
}
