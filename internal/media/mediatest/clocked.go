// Package mediatest builds recordings for tests outside package media.
package mediatest

import (
	"encoding/binary"
	"os"
	"testing"
	"time"
)

// mp4Epoch is where MP4 creation times count from (1904-01-01 UTC).
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// Clocked is a minimal MP4 (ftyp and a moov holding only mvhd) whose movie
// header says it was recorded at at, as Voice Memos writes it. It has no
// track, so its length cannot be read: its recorded span is the one moment.
func Clocked(at time.Time) []byte {
	mvhd := make([]byte, 100) // version 0
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(at.Sub(mp4Epoch)/time.Second))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(at.Sub(mp4Epoch)/time.Second))
	binary.BigEndian.PutUint32(mvhd[12:16], 1000) // timescale
	ftyp := box("ftyp", []byte("M4A \x00\x00\x00\x00M4A isom"))
	return append(ftyp, box("moov", box("mvhd", mvhd))...)
}

// WriteClocked writes Clocked(at) to path.
func WriteClocked(t testing.TB, path string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, Clocked(at), 0o600); err != nil {
		t.Fatal(err)
	}
}

func box(typ string, body []byte) []byte {
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}
