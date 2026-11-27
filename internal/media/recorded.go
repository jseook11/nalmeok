package media

import (
	"encoding/binary"
	"os"
	"time"
)

// mp4Epoch is where MP4 creation times count from (1904-01-01 UTC).
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// recordedAt is when the file at path was recorded: an MP4-family file's
// movie header creation time, which Voice Memos and most recorders set,
// when it is plausible. Nothing else is a recording time: a file without
// one (a lecture video downloaded from the LMS, 2026-11-27) has only its
// modification time, which is when it was downloaded or copied. Counted as
// the recording, it put downloads in the week they were fetched and in the
// class that was on then.
func recordedAt(path string, info os.FileInfo) (time.Time, bool) {
	if FormatOf(path) != FormatAACMP4 {
		return time.Time{}, false
	}
	t, ok := mp4CreationTime(path)
	if !ok || !t.Before(info.ModTime().Add(time.Hour)) {
		return time.Time{}, false
	}
	return t, true
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

// RecordedSpan is when the audio at path started and stopped, from its
// creation time (recordedAt) to that plus its length. For a folder it runs
// from its first file's start to its last file's end, over the files that
// have a creation time. ok is false when no file has one. When the length
// cannot be read, start and end are the same moment.
func RecordedSpan(path string) (start, end time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if !info.IsDir() {
		return fileRecordedSpan(path, info)
	}
	audio, _, err := listFolder(path)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	for _, p := range dropConvertedMP4(audio) {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		s, e, ok := fileRecordedSpan(p, st)
		if !ok {
			continue
		}
		if start.IsZero() || s.Before(start) {
			start = s
		}
		if e.After(end) {
			end = e
		}
	}
	return start, end, !start.IsZero()
}

func fileRecordedSpan(path string, info os.FileInfo) (time.Time, time.Time, bool) {
	at, ok := recordedAt(path, info)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	seconds, err := Duration(path)
	if err != nil || seconds <= 0 {
		return at, at, true
	}
	return at, at.Add(time.Duration(seconds * float64(time.Second))), true
}
