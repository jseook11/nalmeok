// Package media scans dropped inputs, reads audio durations, and prepares
// upload-ready audio pieces without re-encoding.
//
//   - .m4a/.mp4/.mov/.m4b/.m4v/.3gp (AAC, plain or fragmented): the audio
//     track is remuxed into a minimal audio-only M4A (see m4a.go). MP3 audio
//     inside such a container is written out as plain MP3 frames.
//   - .aac (ADTS): the frames are remuxed into M4A the same way.
//   - .mp3: uploaded as-is, split at MP3 frame boundaries.
//   - .wav (RIFF/RF64), .aif/.aiff/.aifc: uploaded as-is, split at sample
//     boundaries with a fresh header.
//   - .flac: uploaded as-is, split at FLAC frame boundaries.
//   - .ogg/.opus (Opus or Vorbis): uploaded as-is, split at Ogg page
//     boundaries with the codec headers repeated in every piece.
package media

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// Track formats. FormatAACMP4 covers every ISO-BMFF container; its value is
// kept for saved items.
const (
	FormatAACMP4 = "aac-mp4"
	FormatADTS   = "adts"
	FormatMP3    = "mp3"
	FormatWAV    = "wav"
	FormatAIFF   = "aiff"
	FormatFLAC   = "flac"
	FormatOgg    = "ogg"
)

// formats maps lower-case file extensions to track formats.
var formats = map[string]string{
	".m4a": FormatAACMP4, ".mp4": FormatAACMP4, ".mov": FormatAACMP4,
	".m4b": FormatAACMP4, ".m4v": FormatAACMP4, ".3gp": FormatAACMP4,
	".aac": FormatADTS,
	".mp3": FormatMP3,
	".wav": FormatWAV,
	".aif": FormatAIFF, ".aiff": FormatAIFF, ".aifc": FormatAIFF,
	".flac": FormatFLAC,
	".ogg":  FormatOgg, ".opus": FormatOgg,
}

// Extensions lists the accepted audio file extensions ("m4a", ...), sorted.
func Extensions() []string {
	out := make([]string, 0, len(formats))
	for ext := range formats {
		out = append(out, ext[1:])
	}
	sort.Strings(out)
	return out
}

// MaxFiles is the largest number of audio files a folder may hold.
const MaxFiles = 10

// Track is one audio file of an input.
type Track struct {
	Path    string
	Format  string // one of the Format constants
	Seconds float64
}

// Input is what the user dropped: a single audio file or a folder.
type Input struct {
	Source string // absolute path
	Kind   string // "file" | "folder"
	Name   string // display name
	Tracks []Track
	// PDFs are the reference PDFs directly inside a dropped folder, in
	// natural order. A file input has none. Which of them the requests use
	// is the item's choice (see runner).
	PDFs []string
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
var ErrUnsupported = i18n.Error("media.unsupported")

// FormatOf returns the track format for a file name, or "" if unsupported.
func FormatOf(name string) string {
	return formats[strings.ToLower(filepath.Ext(name))]
}

// Scan reads a dropped file or folder (spec §3-1).
func Scan(path string) (Input, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Input{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Input{}, i18n.Error("media.openFailed", "name", filepath.Base(abs))
	}
	if !info.IsDir() {
		format := FormatOf(abs)
		if format == "" {
			return Input{}, fmt.Errorf("%w: %s", ErrUnsupported, filepath.Base(abs))
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
		return Input{}, i18n.Error("media.readDirFailed", "name", filepath.Base(abs))
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
		case IsPDF(name):
			pdfs = append(pdfs, full)
		}
	}
	audio = dropConvertedMP4(audio)
	if len(audio) == 0 {
		return Input{}, i18n.Error("media.noAudio")
	}
	if len(audio) > MaxFiles {
		return Input{}, i18n.Error("media.tooMany", "count", len(audio), "max", MaxFiles)
	}
	SortNatural(audio)
	SortNatural(pdfs)
	in := Input{Source: abs, Kind: "folder", Name: filepath.Base(abs), PDFs: pdfs}
	for _, p := range audio {
		track, err := readTrack(p, FormatOf(p))
		if err != nil {
			return Input{}, err
		}
		in.Tracks = append(in.Tracks, track)
	}
	return in, nil
}

// IsPDF reports whether name has a .pdf extension (any case).
func IsPDF(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".pdf")
}

// FolderPDFs lists the PDFs directly inside dir in natural order, the same
// ones Scan finds: hidden files and anything that is not a regular file are
// skipped, subfolders are not searched. A path that is not a readable folder
// has none.
func FolderPDFs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var pdfs []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !IsPDF(name) {
			continue
		}
		full := filepath.Join(dir, name)
		if st, err := os.Stat(full); err != nil || !st.Mode().IsRegular() {
			continue
		}
		pdfs = append(pdfs, full)
	}
	SortNatural(pdfs)
	return pdfs
}

// dropConvertedMP4 removes a video (.mp4, .mov, .m4v, .3gp) when an .m4a
// with the same name sits next to it: the .m4a is the audio already taken
// out of that video, and sending both would transcribe (and bill) the
// lecture twice. The Python app did the same for .mp4. Names compare
// case-insensitively, as on macOS and Windows disks.
var videoExt = map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".3gp": true}

func dropConvertedMP4(paths []string) []string {
	m4a := make(map[string]bool)
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".m4a") {
			m4a[strings.ToLower(strings.TrimSuffix(p, filepath.Ext(p)))] = true
		}
	}
	out := paths[:0:0]
	for _, p := range paths {
		ext := filepath.Ext(p)
		if videoExt[strings.ToLower(ext)] && m4a[strings.ToLower(strings.TrimSuffix(p, ext))] {
			continue
		}
		out = append(out, p)
	}
	return out
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
	case FormatADTS:
		t, err := parseADTS(path)
		if err != nil {
			return 0, err
		}
		return t.seconds(), nil
	case FormatMP3:
		return mp3Duration(path)
	case FormatWAV:
		w, err := parseWAV(path)
		if err != nil {
			return 0, err
		}
		return w.seconds(), nil
	case FormatAIFF:
		return aiffDuration(path)
	case FormatFLAC:
		return flacDuration(path)
	case FormatOgg:
		return oggDuration(path)
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
	case FormatADTS:
		return prepareADTS(t.Path, dir, maxSeconds)
	case FormatMP3:
		return prepareMP3(t.Path, dir, maxSeconds)
	case FormatWAV:
		return prepareWAV(t.Path, dir, maxSeconds)
	case FormatAIFF:
		return prepareAIFF(t.Path, dir, maxSeconds)
	case FormatFLAC:
		return prepareFLAC(t.Path, dir, maxSeconds)
	case FormatOgg:
		return prepareOgg(t.Path, dir, maxSeconds)
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
