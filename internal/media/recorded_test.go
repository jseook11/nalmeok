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

// A file with a video track is a downloaded lecture, not a recording: it has
// no recording time even when its header has a creation time, and a folder
// spans only its audio.
func TestRecordedSpanSkipsVideo(t *testing.T) {
	fx := fixtures(t)
	if _, _, ok := RecordedSpan(filepath.Join(fx, "x.mp4")); !ok {
		t.Fatal("audio-only mp4 should have a recording time")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "lecture.mp4")
	if err := os.WriteFile(video, withVideoTrack(t, filepath.Join(fx, "x.mp4")), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, e, ok := RecordedSpan(video); ok {
		t.Fatalf("video span = %v – %v, want none", s, e)
	}
	if _, _, ok := RecordedSpan(dir); ok {
		t.Fatal("folder of videos should have no recording time")
	}

	stop := time.Date(2026, 11, 10, 15, 2, 0, 0, time.Local)
	wav := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(wav, pcmWAVBytes(120), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(wav, stop, stop); err != nil {
		t.Fatal(err)
	}
	start, end, ok := RecordedSpan(dir)
	if !ok || !start.Equal(stop.Add(-2*time.Minute)) || !end.Equal(stop) {
		t.Fatalf("folder span = %v – %v, %v; want the wav alone", start, end, ok)
	}
}
