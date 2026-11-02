// Package media scans dropped inputs, reads audio durations, and prepares
// upload-ready audio pieces without re-encoding.
//
//   - .m4a/.mp4 (AAC): raw AAC access units are pulled out of the ISO-BMFF
//     container and written as ADTS (.aac, audio/aac).
//   - .mp3: uploaded as-is, split at MP3 frame boundaries.
//   - .wav: uploaded as-is, split at sample boundaries with a fresh RIFF header.
package media

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Track formats.
const (
	FormatAACMP4 = "aac-mp4"
	FormatMP3    = "mp3"
	FormatWAV    = "wav"
)

// MaxFiles is the largest number of audio files a folder may hold.
const MaxFiles = 10

// Track is one audio file of an input.
type Track struct {
	Path    string
	Format  string // "aac-mp4", "mp3", "wav"
	Seconds float64
}

// Input is what the user dropped: a single audio file or a folder.
type Input struct {
	Source string // absolute path
	Kind   string // "file" | "folder"
	Name   string // display name
	Tracks []Track
	PDF    string // optional reference PDF (folder only)
}

// Seconds is the total audio length.
func (in Input) Seconds() float64 {
	total := 0.0
	for _, t := range in.Tracks {
		total += t.Seconds
	}
	return total
}

// Piece is one upload-ready audio file.
type Piece struct {
	Path    string
	MIME    string
	Seconds float64
}

// ErrUnsupported marks inputs this app cannot read.
var ErrUnsupported = errors.New("지원하지 않는 형식입니다")

// FormatOf returns the track format for a file name, or "" if unsupported.
func FormatOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".m4a", ".mp4":
		return FormatAACMP4
	case ".mp3":
		return FormatMP3
	case ".wav":
		return FormatWAV
	}
	return ""
}

// Scan reads a dropped file or folder (spec §3-1).
func Scan(path string) (Input, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Input{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Input{}, fmt.Errorf("입력을 열 수 없습니다: %s", filepath.Base(abs))
	}
	if !info.IsDir() {
		format := FormatOf(abs)
		if format == "" {
			return Input{}, fmt.Errorf("%w: %s (.m4a, .mp4, .mp3, .wav만 받습니다)", ErrUnsupported, filepath.Base(abs))
		}
		track, err := readTrack(abs, format)
		if err != nil {
			return Input{}, err
		}
		name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
		return Input{Source: abs, Kind: "file", Name: name, Tracks: []Track{track}}, nil
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return Input{}, fmt.Errorf("폴더를 읽을 수 없습니다: %s", filepath.Base(abs))
	}
	var audio, pdfs []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue // hidden files, AppleDouble "._x.m4a"
		}
		full := filepath.Join(abs, name)
		st, err := os.Stat(full) // follow symlinks
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		switch {
		case FormatOf(name) != "":
			audio = append(audio, full)
		case strings.EqualFold(filepath.Ext(name), ".pdf"):
			pdfs = append(pdfs, full)
		}
	}
	if len(audio) == 0 {
		return Input{}, fmt.Errorf("폴더에 오디오 파일이 없습니다 (.m4a, .mp4, .mp3, .wav): %s", filepath.Base(abs))
	}
	if len(audio) > MaxFiles {
		return Input{}, fmt.Errorf("오디오 파일은 1~%d개여야 합니다 (현재 %d개).", MaxFiles, len(audio))
	}
	if len(pdfs) > 1 {
		return Input{}, errors.New("참고 PDF는 폴더에 최대 1개까지 둘 수 있습니다.")
	}
	SortNatural(audio)
	in := Input{Source: abs, Kind: "folder", Name: filepath.Base(abs)}
	if len(pdfs) == 1 {
		in.PDF = pdfs[0]
	}
	for _, p := range audio {
		track, err := readTrack(p, FormatOf(p))
		if err != nil {
			return Input{}, err
		}
		in.Tracks = append(in.Tracks, track)
	}
	return in, nil
}

func readTrack(path, format string) (Track, error) {
	seconds, err := Duration(path)
	if err != nil {
		return Track{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return Track{Path: path, Format: format, Seconds: seconds}, nil
}

// Duration returns the audio length in seconds.
func Duration(path string) (float64, error) {
	switch FormatOf(path) {
	case FormatAACMP4:
		t, err := parseMP4(path)
		if err != nil {
			return 0, err
		}
		return t.playSeconds(), nil
	case FormatMP3:
		return mp3Duration(path)
	case FormatWAV:
		w, err := parseWAV(path)
		if err != nil {
			return 0, err
		}
		return w.seconds(), nil
	}
	return 0, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Base(path))
}

// Prepare writes upload-ready pieces of t into dir, each at most maxSeconds
// long (maxSeconds <= 0 means no limit). Piece names are
// "<source file name>.NNN.<ext>", so tracks of one folder never collide.
func Prepare(t Track, dir string, maxSeconds float64) ([]Piece, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	format := t.Format
	if format == "" {
		format = FormatOf(t.Path)
	}
	switch format {
	case FormatAACMP4:
		return prepareMP4(t.Path, dir, maxSeconds)
	case FormatMP3:
		return prepareMP3(t.Path, dir, maxSeconds)
	case FormatWAV:
		return prepareWAV(t.Path, dir, maxSeconds)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Base(t.Path))
}

func piecePath(dir, source string, index int, ext string) string {
	return filepath.Join(dir, fmt.Sprintf("%s.%03d%s", filepath.Base(source), index, ext))
}

// pieceWriter writes one piece to a temp file and publishes it on Close.
type pieceWriter struct {
	f    *os.File
	path string
}

func createPiece(path string) (*pieceWriter, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".piece-*")
	if err != nil {
		return nil, err
	}
	return &pieceWriter{f: f, path: path}, nil
}

func (p *pieceWriter) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *pieceWriter) commit() error {
	name := p.f.Name()
	if err := p.f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p.path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func (p *pieceWriter) abort() {
	name := p.f.Name()
	p.f.Close()
	os.Remove(name)
}

// linkOrCopy publishes src at dst, preferring a hard link.
func linkOrCopy(src, dst string) error {
	os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := createPiece(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		w.abort()
		return err
	}
	return w.commit()
}

// SortNatural sorts file paths by base name so that "2강" < "10강".
func SortNatural(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		return NaturalLess(filepath.Base(paths[i]), filepath.Base(paths[j]))
	})
}

// NaturalLess mirrors Python natural_key: runs of ASCII digits compare as
// numbers, everything else compares case-insensitively.
func NaturalLess(a, b string) bool {
	ka, kb := naturalKey(a), naturalKey(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] == kb[i] {
			continue
		}
		if i%2 == 1 { // numeric run
			if c := numCompare(ka[i], kb[i]); c != 0 {
				return c < 0
			}
			continue
		}
		return ka[i] < kb[i]
	}
	if len(ka) != len(kb) {
		return len(ka) < len(kb)
	}
	return a < b
}

// naturalKey alternates text and digit runs, always starting with text
// (possibly empty), like re.split(r"(\d+)", s).
func naturalKey(s string) []string {
	s = strings.ToLower(s)
	var parts []string
	start := 0
	for i := 0; i < len(s); {
		if !isDigit(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		parts = append(parts, s[start:i], s[i:j])
		start, i = j, j
	}
	return append(parts, s[start:])
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func numCompare(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}
