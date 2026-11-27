package media

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/media/mediatest"
)

// clockedCopy copies the m4a fixture to dir/name with its movie header
// saying it was recorded at at, or, for the zero time, leaving it unset as
// the LMS's downloads do.
func clockedCopy(t *testing.T, dir, name string, at time.Time) string {
	t.Helper()
	data := mustRead(t, filepath.Join(fixtures(t), "x.m4a"))
	i := bytes.Index(data, []byte("mvhd"))
	if i < 0 {
		t.Fatal("fixture has no mvhd")
	}
	var secs uint32
	if !at.IsZero() {
		secs = uint32(at.Sub(mp4Epoch) / time.Second)
	}
	switch data[i+4] { // version
	case 0:
		binary.BigEndian.PutUint32(data[i+8:], secs)
	default:
		binary.BigEndian.PutUint64(data[i+8:], uint64(secs))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A recording runs from its movie header's creation time for its length; a
// folder spans its first start to last end.
func TestRecordedSpan(t *testing.T) {
	dir := t.TempDir()
	rec := time.Date(2026, 11, 10, 13, 0, 0, 0, time.Local)
	one := clockedCopy(t, dir, "a.m4a", rec)
	length, err := Duration(one)
	if err != nil || length <= 0 {
		t.Fatalf("fixture length = %v, %v", length, err)
	}
	stop := rec.Add(time.Duration(length * float64(time.Second)))
	start, end, ok := RecordedSpan(one)
	if !ok || !start.Equal(rec) || !end.Equal(stop) {
		t.Fatalf("file span = %v – %v, %v", start, end, ok)
	}

	later := rec.Add(10 * time.Minute)
	clockedCopy(t, dir, "b.m4a", later)
	start, end, ok = RecordedSpan(dir)
	if !ok || !start.Equal(rec) || !end.Equal(later.Add(stop.Sub(rec))) {
		t.Fatalf("folder span = %v – %v, %v", start, end, ok)
	}

	// Unreadable length: the one moment.
	moment := filepath.Join(t.TempDir(), "c.m4a")
	mediatest.WriteClocked(t, moment, rec)
	start, end, ok = RecordedSpan(moment)
	if !ok || !start.Equal(rec) || !end.Equal(rec) {
		t.Fatalf("unreadable span = %v – %v, %v", start, end, ok)
	}
}

// A file with no creation time (a lecture video downloaded from the LMS, a
// WAV) has no recording time: its modification time is when it was
// downloaded or copied. A folder counts only the files that have one.
func TestRecordedSpanNeedsCreationTime(t *testing.T) {
	dir := t.TempDir()
	stop := time.Date(2026, 11, 27, 17, 10, 0, 0, time.Local)
	clockedCopy(t, dir, "algorithm_13.1.mp4", time.Time{})
	if err := os.WriteFile(filepath.Join(dir, "a.wav"), pcmWAVBytes(120), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"algorithm_13.1.mp4", "a.wav"} {
		path := filepath.Join(dir, name)
		if err := os.Chtimes(path, stop, stop); err != nil {
			t.Fatal(err)
		}
		if _, ok := mp4CreationTime(path); ok {
			t.Fatalf("%s has a creation time", name)
		}
		if start, end, ok := RecordedSpan(path); ok {
			t.Errorf("%s span = %v – %v, want none", name, start, end)
		}
	}
	if start, end, ok := RecordedSpan(dir); ok {
		t.Errorf("folder without creation times spans %v – %v", start, end)
	}
	rec := time.Date(2026, 11, 10, 13, 0, 0, 0, time.Local)
	mediatest.WriteClocked(t, filepath.Join(dir, "rec.m4a"), rec)
	if start, end, ok := RecordedSpan(dir); !ok || !start.Equal(rec) || !end.Equal(rec) {
		t.Errorf("mixed folder span = %v – %v, %v; want the recording's", start, end, ok)
	}
}
