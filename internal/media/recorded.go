package media

import (
	"encoding/binary"
	"os"
	"time"
)

// mp4Epoch is where MP4 creation times count from (1904-01-01 UTC).
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// RecordedAt is when the audio at path (a file, or a folder of audio files)
// was recorded. An MP4-family file's movie header creation time wins when it
// is plausible (Voice Memos and most recorders set it); otherwise the
// file's modification time, which a recorder writes when it stops. For a
// folder it is the earliest of its audio files. ok is false when nothing
// could be read.
func RecordedAt(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	if !info.IsDir() {
		return fileRecordedAt(path, info), true
	}
	audio, _, err := listFolder(path)
	if err != nil {
		return time.Time{}, false
	}
	var first time.Time
	for _, p := range dropConvertedMP4(audio) {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if t := fileRecordedAt(p, st); first.IsZero() || t.Before(first) {
			first = t
		}
	}
	return first, !first.IsZero()
}

func fileRecordedAt(path string, info os.FileInfo) time.Time {
	if FormatOf(path) == FormatAACMP4 {
		if t, ok := mp4CreationTime(path); ok && t.Before(info.ModTime().Add(time.Hour)) {
			return t
		}
	}
	return info.ModTime()
}

// mp4CreationTime reads mvhd's creation time. Zero, pre-2010 and future
// values (unset or bogus headers) are rejected.
func mp4CreationTime(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	moov, _, err := readMoov(f)
	if err != nil {
		return time.Time{}, false
	}
	mvhd, ok := findBox(moov, "mvhd")
	if !ok || len(mvhd) < 12 {
		return time.Time{}, false
	}
	var secs uint64
	if mvhd[0] == 1 {
		secs = binary.BigEndian.Uint64(mvhd[4:12])
	} else {
		secs = uint64(binary.BigEndian.Uint32(mvhd[4:8]))
	}
	t := mp4Epoch.Add(time.Duration(secs) * time.Second)
	if t.Year() < 2010 || t.After(time.Now().Add(24*time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}
