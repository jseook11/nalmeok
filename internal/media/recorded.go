package media

import (
	"encoding/binary"
	"os"
	"time"
)

// mp4Epoch is where MP4 creation times count from (1904-01-01 UTC).
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// fileRecordedAt is when one file was recorded: an MP4-family file's movie
// header creation time when it is plausible (Voice Memos and most recorders
// set it), otherwise the modification time, which a recorder writes when it
// stops.
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

// RecordedSpan is when the audio at path started and stopped: the MP4
// creation time plus the length when the file has one, otherwise the
// modification time (when the recorder stopped) minus the length. For a
// folder it runs from its first file's start to its last file's end. When
// the length cannot be read, start and end are the same moment, the one
// fileRecordedAt gives. A video (hasVideo) has no recording time: ok is
// false for it, and a folder leaves it out.
func RecordedSpan(path string) (start, end time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if !info.IsDir() {
		if hasVideo(path) {
			return time.Time{}, time.Time{}, false
		}
		start, end = fileRecordedSpan(path, info)
		return start, end, true
	}
	audio, _, err := listFolder(path)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	for _, p := range dropConvertedMP4(audio) {
		st, err := os.Stat(p)
		if err != nil || hasVideo(p) {
			continue
		}
		s, e := fileRecordedSpan(p, st)
		if start.IsZero() || s.Before(start) {
			start = s
		}
		if e.After(end) {
			end = e
		}
	}
	return start, end, !start.IsZero()
}

// hasVideo reports whether the MP4-family file at path has a video track.
// Such a file is a lecture video someone downloaded, not a class recorded
// on a phone or laptop (nobody films a class to transcribe it), and its
// times are when it was exported or downloaded (2026-11-27: e-class videos
// got the week and the class of their download). It has no recording time.
func hasVideo(path string) bool {
	if FormatOf(path) != FormatAACMP4 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	moov, _, err := readMoov(f)
	if err != nil {
		return false
	}
	traks, err := childBoxes(moov)
	if err != nil {
		return false
	}
	for _, trak := range traks {
		if trak.typ != "trak" {
			continue
		}
		if hdlr, ok := findBox(trak.body, "mdia", "hdlr"); ok && len(hdlr) >= 12 && string(hdlr[8:12]) == "vide" {
			return true
		}
	}
	return false
}

func fileRecordedSpan(path string, info os.FileInfo) (time.Time, time.Time) {
	seconds, err := Duration(path)
	length := time.Duration(seconds * float64(time.Second))
	if err != nil || seconds <= 0 {
		at := fileRecordedAt(path, info)
		return at, at
	}
	if FormatOf(path) == FormatAACMP4 {
		if t, ok := mp4CreationTime(path); ok && t.Before(info.ModTime().Add(time.Hour)) {
			return t, t.Add(length)
		}
	}
	return info.ModTime().Add(-length), info.ModTime()
}
