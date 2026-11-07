package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// ---- fixtures ---------------------------------------------------------------

var (
	fixtureOnce sync.Once
	fixtureDir  string
	fixtureErr  error
)

// fixtureSeconds is the length of the generated source audio: long enough
// to cut into several 2-second pieces.
const fixtureSeconds = 12

// sourceWAV is fixtureSeconds of 16-bit mono 22.05 kHz audio: a tone that
// glides and pulses so the AAC encoder has real work to do. It is generated
// here instead of with `say`, whose output length depends on the installed
// voices.
func sourceWAV() []byte {
	const rate = 22050
	n := fixtureSeconds * rate
	b := make([]byte, 44, 44+2*n)
	copy(b, "RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00")
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*n))
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*n))
	phase := 0.0
	for i := range n {
		sec := float64(i) / rate
		phase += 2 * math.Pi * (300 + 200*math.Sin(sec)) / rate
		amp := 0.4 * (0.6 + 0.4*math.Sin(2*math.Pi*3*sec))
		b = binary.LittleEndian.AppendUint16(b, uint16(int16(amp*32767*math.Sin(phase))))
	}
	return b
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fixtureDir != "" {
		os.RemoveAll(fixtureDir)
	}
	os.Exit(code)
}

func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s 없음: 이 테스트는 macOS 도구가 필요합니다", name)
		}
	}
}

// fixtures returns a shared directory with x.m4a, x.mp4, x.wav, x-alac.m4a.
func fixtures(t *testing.T) string {
	t.Helper()
	needTools(t, "afconvert", "afinfo")
	fixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nalmeok-media-")
		if err != nil {
			fixtureErr = err
			return
		}
		fixtureDir = dir
		aiff := filepath.Join(dir, "x.aiff")
		src := filepath.Join(dir, "source.wav")
		if fixtureErr = os.WriteFile(src, sourceWAV(), 0o644); fixtureErr != nil {
			return
		}
		cmds := [][]string{
			{"afconvert", "-f", "AIFF", "-d", "BEI16", src, aiff},
			{"afconvert", "-f", "m4af", "-d", "aac", aiff, filepath.Join(dir, "x.m4a")},
			{"afconvert", "-f", "mp4f", "-d", "aac", aiff, filepath.Join(dir, "x.mp4")},
			{"afconvert", "-f", "WAVE", "-d", "LEI16", aiff, filepath.Join(dir, "x.wav")},
			{"afconvert", "-f", "m4af", "-d", "alac", aiff, filepath.Join(dir, "x-alac.m4a")},
		}
		for _, c := range cmds {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				fixtureErr = errors.New(strings.Join(c, " ") + ": " + string(out))
				return
			}
		}
	})
	if fixtureErr != nil {
		t.Skipf("fixture 생성 실패: %v", fixtureErr)
	}
	return fixtureDir
}

var afinfoDurationRE = regexp.MustCompile(`estimated duration: ([0-9.]+) sec`)

// afinfoDuration runs afinfo (which must succeed) and returns its duration.
func afinfoDuration(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("afinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("afinfo %s: %v\n%s", filepath.Base(path), err, out)
	}
	m := afinfoDurationRE.FindSubmatch(out)
	if m == nil {
		t.Fatalf("afinfo %s: duration 없음\n%s", filepath.Base(path), out)
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---- natural sort -----------------------------------------------------------

func TestNaturalSort(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"10강.m4a", "2강.m4a", "1강.m4a"}, []string{"1강.m4a", "2강.m4a", "10강.m4a"}},
		// same as Python natural_key: "a" < "a.mp3", so "a2" sorts before "A"
		{[]string{"b.mp3", "A.mp3", "a2.mp3", "a10.mp3"}, []string{"a2.mp3", "a10.mp3", "A.mp3", "b.mp3"}},
		{[]string{"lec 02.wav", "lec 1.wav", "lec 3.wav"}, []string{"lec 1.wav", "lec 02.wav", "lec 3.wav"}},
		{[]string{"part1-10.m4a", "part1-9.m4a", "part1.m4a"}, []string{"part1-9.m4a", "part1-10.m4a", "part1.m4a"}},
		{[]string{"03.m4a", "3.m4a", "2.m4a"}, []string{"2.m4a", "03.m4a", "3.m4a"}},
		{[]string{"/x/3장.m4a", "/y/12장.m4a", "/z/2장.m4a"}, []string{"/z/2장.m4a", "/x/3장.m4a", "/y/12장.m4a"}},
	}
	for _, c := range cases {
		got := slices.Clone(c.in)
		SortNatural(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("SortNatural(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if !NaturalLess("2강", "10강") || NaturalLess("10강", "2강") {
		t.Error("2강 < 10강 이어야 합니다")
	}
}

// ---- Scan -------------------------------------------------------------------

func touch(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanErrors(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"empty", nil, i18n.T("media.noAudio")},
		{"only pdf and text", []string{"a.pdf", "note.txt"}, i18n.T("media.noAudio")},
		{"eleven", []string{"1.mp3", "2.mp3", "3.mp3", "4.mp3", "5.mp3", "6.mp3", "7.mp3", "8.mp3", "9.mp3", "10.mp3", "11.mp3"}, i18n.T("media.tooMany", "count", 11, "max", 10)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			touch(t, dir, c.files...)
			_, err := Scan(dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Scan err = %v, want %q", err, c.want)
			}
		})
	}

	dir := t.TempDir()
	touch(t, dir, "slides.pptx")
	if _, err := Scan(filepath.Join(dir, "slides.pptx")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported file: err = %v", err)
	}
	touch(t, dir, "broken.mp3")
	if _, err := Scan(filepath.Join(dir, "broken.mp3")); err == nil {
		t.Error("broken mp3 should fail")
	}
}

func link(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFolder(t *testing.T) {
	fx := fixtures(t)
	dir := filepath.Join(t.TempDir(), "강의")
	os.Mkdir(dir, 0o755)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	link(t, filepath.Join(fx, "x.m4a"), filepath.Join(dir, "10강.m4a"))
	link(t, filepath.Join(fx, "x.wav"), filepath.Join(dir, "2강.wav"))
	link(t, filepath.Join(fx, "x.mp4"), filepath.Join(dir, "1강.mp4"))
	link(t, filepath.Join(fx, "x.m4a"), filepath.Join(dir, "sub", "3강.m4a"))
	touch(t, dir, "용어.pdf", "메모.txt", "._10강.m4a")

	in, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "folder" || in.Name != "강의" || in.Source != dir {
		t.Errorf("input = %+v", in)
	}
	if !slices.Equal(in.PDFs, []string{filepath.Join(dir, "용어.pdf")}) {
		t.Errorf("PDFs = %q", in.PDFs)
	}
	var names, formats []string
	for _, tr := range in.Tracks {
		names = append(names, filepath.Base(tr.Path))
		formats = append(formats, tr.Format)
	}
	if !slices.Equal(names, []string{"1강.mp4", "2강.wav", "10강.m4a"}) {
		t.Errorf("tracks = %v", names)
	}
	if !slices.Equal(formats, []string{FormatAACMP4, FormatWAV, FormatAACMP4}) {
		t.Errorf("formats = %v", formats)
	}
	if want := 3 * afinfoDuration(t, filepath.Join(fx, "x.m4a")); !near(in.Seconds(), want, 0.3) {
		t.Errorf("Seconds = %v, want ≈ %v", in.Seconds(), want)
	}

	file, err := Scan(filepath.Join(dir, "10강.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != "file" || file.Name != "10강" || len(file.Tracks) != 1 || len(file.PDFs) != 0 {
		t.Errorf("file input = %+v", file)
	}
}

// ---- Duration / Prepare against afinfo ----------------------------------------

func TestDurationMatchesAfinfo(t *testing.T) {
	fx := fixtures(t)
	for _, name := range []string{"x.m4a", "x.mp4", "x.wav"} {
		p := filepath.Join(fx, name)
		got, err := Duration(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := afinfoDuration(t, p); !near(got, want, 0.1) {
			t.Errorf("%s: Duration = %.3f, afinfo = %.3f", name, got, want)
		}
	}
}

func TestNonAACMP4(t *testing.T) {
	fx := fixtures(t)
	_, err := Duration(filepath.Join(fx, "x-alac.m4a"))
	if !errors.Is(err, ErrNotAAC) {
		t.Fatalf("err = %v, want ErrNotAAC", err)
	}
	if !strings.Contains(err.Error(), i18n.T("media.mp4NotAAC")) {
		t.Errorf("message = %q", err)
	}
}

func TestFragmentedMP4(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"mvex.m4a": slices.Concat(mp4Box("ftyp", []byte("iso6\x00\x00\x00\x00")), mp4Box("moov", mp4Box("mvex"))),
		"moof.mp4": slices.Concat(mp4Box("ftyp", []byte("iso6\x00\x00\x00\x00")), mp4Box("moov"), mp4Box("moof"), mp4Box("mdat")),
		"none.m4a": mp4Box("ftyp", []byte("M4A \x00\x00\x00\x00")),
	}
	for name, data := range cases {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		if _, err := Duration(p); err == nil || !strings.Contains(err.Error(), "MP4") {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := Prepare(Track{Path: p, Format: FormatAACMP4}, dir, 2); err == nil {
			t.Errorf("%s: Prepare should fail", name)
		}
	}
}

func checkPieces(t *testing.T, pieces []Piece, total, maxSeconds float64, mime string) {
	t.Helper()
	if len(pieces) < 2 {
		t.Fatalf("pieces = %d, want several", len(pieces))
	}
	sum := 0.0
	for i, p := range pieces {
		if p.MIME != mime {
			t.Errorf("piece %d MIME = %q", i, p.MIME)
		}
		if p.Seconds > maxSeconds+1e-9 || p.Seconds <= 0 {
			t.Errorf("piece %d Seconds = %v (max %v)", i, p.Seconds, maxSeconds)
		}
		if got := afinfoDuration(t, p.Path); !near(got, p.Seconds, 0.1) {
			t.Errorf("piece %d: afinfo %.3f vs Seconds %.3f", i, got, p.Seconds)
		}
		sum += p.Seconds
	}
	if !near(sum, total, 0.1) {
		t.Errorf("sum of pieces %.3f, want ≈ %.3f", sum, total)
	}
}

func TestPrepareMP4(t *testing.T) {
	fx := fixtures(t)
	for _, name := range []string{"x.m4a", "x.mp4"} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(fx, name)
			track, err := parseMP4(src)
			if err != nil {
				t.Fatal(err)
			}
			total := track.seconds() // pieces keep priming/padding frames
			if played, _ := Duration(src); played > total || total-played > 0.2 {
				t.Errorf("Duration %v vs coded %v", played, total)
			}
			dir := t.TempDir()
			pieces, err := Prepare(Track{Path: src, Format: FormatAACMP4}, dir, 2)
			if err != nil {
				t.Fatal(err)
			}
			checkPieces(t, pieces, total, 2, "audio/m4a")
			for _, p := range pieces {
				if filepath.Dir(p.Path) != dir {
					t.Errorf("path = %s", p.Path)
				}
			}
			checkM4A(t, src, pieces)

			whole, err := Prepare(Track{Path: src, Format: FormatAACMP4}, t.TempDir(), 0)
			if err != nil || len(whole) != 1 || !near(whole[0].Seconds, total, 1e-9) {
				t.Fatalf("unlimited Prepare = %+v, %v", whole, err)
			}
			checkM4A(t, src, whole)
		})
	}
}

// checkM4A verifies remuxed pieces: .m4a, faststart layout with one audio
// track, the source sample entry verbatim, the source AAC frames byte for
// byte in order, and decodable by afconvert.
func checkM4A(t *testing.T, src string, pieces []Piece) {
	t.Helper()
	srcTrack, err := parseMP4(src)
	if err != nil {
		t.Fatal(err)
	}
	srcData := mustRead(t, src)
	next := 0
	for i, p := range pieces {
		if filepath.Ext(p.Path) != ".m4a" || p.MIME != "audio/m4a" {
			t.Errorf("piece %d: path %s MIME %s", i, p.Path, p.MIME)
		}
		data := mustRead(t, p.Path)
		top, err := childBoxes(data)
		if err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		var order []string
		for _, b := range top {
			order = append(order, b.typ)
		}
		if !slices.Equal(order, []string{"ftyp", "moov", "mdat"}) {
			t.Errorf("piece %d: boxes %v", i, order)
		}
		moov, _ := findBox(data, "moov")
		traks := 0
		inner, _ := childBoxes(moov)
		for _, b := range inner {
			if b.typ == "trak" {
				traks++
			}
		}
		if traks != 1 {
			t.Errorf("piece %d: %d traks", i, traks)
		}
		pt, err := parseMP4(p.Path)
		if err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		if pt.timescale != srcTrack.timescale || !bytes.Equal(pt.sampleEntry, srcTrack.sampleEntry) {
			t.Errorf("piece %d: timescale/sample entry differ", i)
		}
		if !near(pt.seconds(), p.Seconds, 1e-9) || !near(pt.playSeconds(), p.Seconds, 1e-9) {
			t.Errorf("piece %d: coded %v, played %v, Seconds %v", i, pt.seconds(), pt.playSeconds(), p.Seconds)
		}
		for k := range pt.sizes {
			s := next + k
			a := data[pt.offsets[k] : pt.offsets[k]+int64(pt.sizes[k])]
			b := srcData[srcTrack.offsets[s] : srcTrack.offsets[s]+int64(srcTrack.sizes[s])]
			if !bytes.Equal(a, b) || pt.deltas[k] != srcTrack.deltas[s] {
				t.Fatalf("piece %d sample %d differs from source sample %d", i, k, s)
			}
		}
		next += len(pt.sizes)
		// Without gapless metadata afinfo assumes the standard AAC encoder
		// delay of 2112 samples, so it reports exactly that much less.
		priming := 2112 / float64(srcTrack.timescale)
		if got := afinfoDuration(t, p.Path); !near(got, p.Seconds-priming, 0.001) {
			t.Errorf("piece %d: afinfo %.4f, want Seconds %.4f - priming %.4f", i, got, p.Seconds, priming)
		}
		wav := filepath.Join(t.TempDir(), "out.wav")
		if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", p.Path, wav).CombinedOutput(); err != nil {
			t.Errorf("piece %d: afconvert decode failed: %v\n%s", i, err, out)
		}
	}
	if next != len(srcTrack.sizes) {
		t.Errorf("pieces carry %d samples, source has %d", next, len(srcTrack.sizes))
	}
}

// TestPrepareDropsVideo adds a (fake) video track before the audio track and
// checks that the pieces carry only the audio.
func TestPrepareDropsVideo(t *testing.T) {
	fx := fixtures(t)
	orig := filepath.Join(fx, "x.mp4")
	data := moovAtEnd(t, mustRead(t, orig)) // moov last: growing it moves no chunk
	top, _ := childBoxes(data)
	last := top[len(top)-1]
	if last.typ != "moov" {
		t.Fatal("moov should be last")
	}
	hdlr := slices.Concat([]byte{0, 0, 0, 0}, []byte("vide"), make([]byte, 12), []byte("VideoHandler\x00"))
	video := mp4Box("trak", mp4Box("mdia", fullBox("hdlr", 0, 0, hdlr)))
	children, _ := childBoxes(last.body)
	var body []byte
	body = append(body, mp4Box(children[0].typ, children[0].body)...) // mvhd
	body = append(body, video...)
	for _, c := range children[1:] {
		body = append(body, mp4Box(c.typ, c.body)...)
	}
	moovLen := len(mp4Box("moov", last.body))
	data = slices.Concat(data[:len(data)-moovLen], mp4Box("moov", body))
	src := filepath.Join(t.TempDir(), "video.mp4")
	os.WriteFile(src, data, 0o644)

	pieces, err := Prepare(Track{Path: src, Format: FormatAACMP4}, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	checkM4A(t, src, pieces)
}

func TestPrepareWAV(t *testing.T) {
	fx := fixtures(t)
	src := filepath.Join(fx, "x.wav")
	total, _ := Duration(src)
	pieces, err := Prepare(Track{Path: src, Format: FormatWAV}, t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	checkPieces(t, pieces, total, 2, "audio/wav")
	for _, p := range pieces {
		if _, err := parseWAV(p.Path); err != nil {
			t.Errorf("%s: %v", p.Path, err)
		}
	}
	whole, err := Prepare(Track{Path: src, Format: FormatWAV}, t.TempDir(), 60)
	if err != nil || len(whole) != 1 {
		t.Fatalf("short WAV: %+v, %v", whole, err)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(whole[0].Path)
	if !bytes.Equal(a, b) {
		t.Error("short WAV should be uploaded as-is")
	}
}

// ---- MP3 (synthetic silent frames) -------------------------------------------

// silentMP3 builds an MPEG-1 Layer III 128 kbps 44.1 kHz stereo stream of
// silent frames, optionally with an ID3v2 tag and a Xing/Info frame.
func silentMP3(frames int, id3, xing bool) []byte {
	var b []byte
	if id3 {
		b = append(b, "ID3\x03\x00\x00\x00\x00\x02\x00"...) // 256-byte tag
		b = append(b, make([]byte, 256)...)
	}
	frame := func(pad bool) []byte {
		f := make([]byte, 417)
		copy(f, []byte{0xFF, 0xFB, 0x90, 0x00})
		if pad {
			f[2] |= 0x02
			f = append(f, 0)
		}
		return f
	}
	if xing {
		f := frame(false)
		copy(f[36:], "Info")
		binary.BigEndian.PutUint32(f[40:], 1) // frames flag
		binary.BigEndian.PutUint32(f[44:], uint32(frames))
		b = append(b, f...)
	}
	for i := range frames {
		// pad roughly like a real 128 kbps encoder would
		b = append(b, frame(i%49 < 24)...)
	}
	return append(b, "TAG"+strings.Repeat("\x00", 125)...)
}

func TestMP3(t *testing.T) {
	const frames = 400 // 400 * 1152 / 44100 ≈ 10.449 s
	want := float64(frames*1152) / 44100
	for _, c := range []struct {
		name      string
		id3, xing bool
	}{{"plain", false, false}, {"id3", true, false}, {"xing", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "x.mp3")
			os.WriteFile(src, silentMP3(frames, c.id3, c.xing), 0o644)
			got, err := Duration(src)
			if err != nil {
				t.Fatal(err)
			}
			if !near(got, want, 1e-6) {
				t.Errorf("Duration = %v, want %v", got, want)
			}
			hasAfinfo := exec.Command("afinfo", src).Run() == nil
			if hasAfinfo {
				if a := afinfoDuration(t, src); !near(a, want, 0.1) {
					t.Errorf("afinfo = %v, want %v", a, want)
				}
			}

			out := filepath.Join(dir, "work")
			pieces, err := Prepare(Track{Path: src, Format: FormatMP3}, out, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(pieces) != 6 {
				t.Errorf("pieces = %d, want 6", len(pieces))
			}
			sum := 0.0
			for _, p := range pieces {
				if p.MIME != "audio/mp3" || p.Seconds > 2 {
					t.Errorf("piece %+v", p)
				}
				d, err := Duration(p.Path)
				if err != nil || !near(d, p.Seconds, 1e-6) {
					t.Errorf("piece %s: Duration %v (%v), Seconds %v", p.Path, d, err, p.Seconds)
				}
				if hasAfinfo {
					if a := afinfoDuration(t, p.Path); !near(a, p.Seconds, 0.1) {
						t.Errorf("piece afinfo = %v, Seconds %v", a, p.Seconds)
					}
				}
				sum += p.Seconds
			}
			if !near(sum, want, 1e-6) {
				t.Errorf("sum = %v, want %v", sum, want)
			}

			whole, err := Prepare(Track{Path: src, Format: FormatMP3}, out, 60)
			if err != nil || len(whole) != 1 || filepath.Ext(whole[0].Path) != ".mp3" {
				t.Fatalf("short mp3 = %+v, %v", whole, err)
			}
		})
	}
}

// moovAtEnd rewrites an MP4 as ftyp, mdat, moov (moov last), shifting the
// chunk offsets to match.
func moovAtEnd(t *testing.T, data []byte) []byte {
	t.Helper()
	top, err := childBoxes(data)
	if err != nil {
		t.Fatal(err)
	}
	var ftyp, moov, mdat []byte
	oldMdat, pos := 0, 0
	for _, b := range top {
		r := mp4Box(b.typ, b.body)
		switch b.typ {
		case "ftyp":
			ftyp = r
		case "moov":
			moov = r
		case "mdat":
			mdat, oldMdat = r, pos
		}
		pos += len(r)
	}
	if ftyp == nil || moov == nil || mdat == nil {
		t.Fatal("unexpected layout")
	}
	shift := len(ftyp) - oldMdat // new mdat position - old
	i := bytes.Index(moov, []byte("stco"))
	if i < 0 {
		t.Fatal("stco 없음")
	}
	count := int(binary.BigEndian.Uint32(moov[i+8:]))
	for k := range count {
		p := i + 12 + k*4
		binary.BigEndian.PutUint32(moov[p:], uint32(int(binary.BigEndian.Uint32(moov[p:]))+shift))
	}
	return slices.Concat(ftyp, mdat, moov)
}

func TestMoovAtEnd(t *testing.T) {
	fx := fixtures(t)
	orig := filepath.Join(fx, "x.m4a")
	dir := t.TempDir()
	src := filepath.Join(dir, "tail.m4a")
	os.WriteFile(src, moovAtEnd(t, mustRead(t, orig)), 0o644)
	if data := mustRead(t, src); bytes.Index(data, []byte("moov")) < bytes.Index(data, []byte("mdat")) {
		t.Fatal("moov should be after mdat")
	}
	a, err := Prepare(Track{Path: src, Format: FormatAACMP4}, filepath.Join(dir, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Prepare(Track{Path: orig, Format: FormatAACMP4}, filepath.Join(dir, "b"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, a[0].Path), mustRead(t, b[0].Path)) {
		t.Error("moov-at-end output differs from moov-first output")
	}
	if got, want := afinfoDuration(t, src), afinfoDuration(t, orig); !near(got, want, 0.01) {
		t.Errorf("rewritten file plays %v, want %v", got, want)
	}
}

// writeTemp writes data to a new file name in a test temp dir.
func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- interleaved video ------------------------------------------------------

// countingReaderAt counts the bytes read through it.
type countingReaderAt struct {
	r io.ReaderAt
	n int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}

// interleavedMP4 writes an MP4 whose mdat alternates small audio chunks with
// 2 MiB video chunks, like a screen recording, and returns its path and the
// audio samples in order.
func interleavedMP4(t *testing.T) (string, []byte) {
	t.Helper()
	const chunks, perChunk, videoChunk = 12, 10, 2 << 20
	asc := []byte{0x12, 0x10} // AAC-LC, 44.1 kHz, stereo
	dcd := slices.Concat([]byte{0x40, 0x15, 0, 0, 0}, be(nil).u32(128000).u32(128000), []byte{0x05, byte(len(asc))}, asc)
	es := slices.Concat([]byte{0, 1, 0, 0x04, byte(len(dcd))}, dcd)
	esds := fullBox("esds", 0, 0, []byte{0x03, byte(len(es))}, es)
	mp4a := mp4Box("mp4a", make([]byte, 6), be(nil).u16(1), make([]byte, 8), be(nil).u16(2).u16(16).u32(0).u32(44100<<16), esds)

	ftyp := mp4Box("ftyp", []byte("isom\x00\x00\x00\x00isommp42"))
	base := len(ftyp) + 8 // mdat payload offset
	var mdat, audio []byte
	var sizes, audioChunks, videoChunks be
	for c := range chunks {
		audioChunks = audioChunks.u32(uint32(base + len(mdat)))
		for k := range perChunk {
			s := c*perChunk + k
			size := 300 + s*37%200
			sizes = sizes.u32(uint32(size))
			for b := range size {
				audio = append(audio, byte(s*31+b))
			}
			mdat = append(mdat, audio[len(audio)-size:]...)
		}
		videoChunks = videoChunks.u32(uint32(base + len(mdat)))
		for b := range videoChunk {
			mdat = append(mdat, byte(b*7+c))
		}
	}
	n := uint32(chunks * perChunk)
	handler := func(typ string) []byte {
		return fullBox("hdlr", 0, 0, be(nil).u32(0), []byte(typ), make([]byte, 12), []byte("x\x00"))
	}
	video := mp4Box("trak", mp4Box("mdia", handler("vide"),
		mp4Box("minf", mp4Box("stbl", fullBox("stco", 0, 0, be(nil).u32(chunks), videoChunks)))))
	sound := mp4Box("trak", mp4Box("mdia",
		fullBox("mdhd", 0, 0, be(nil).u32(0).u32(0).u32(44100).u32(n*1024).u16(0x55C4).u16(0)),
		handler("soun"),
		mp4Box("minf", mp4Box("stbl",
			fullBox("stsd", 0, 0, be(nil).u32(1), mp4a),
			fullBox("stts", 0, 0, be(nil).u32(1).u32(n).u32(1024)),
			fullBox("stsc", 0, 0, be(nil).u32(1).u32(1).u32(perChunk).u32(1)),
			fullBox("stsz", 0, 0, be(nil).u32(0).u32(n), sizes),
			fullBox("stco", 0, 0, be(nil).u32(chunks), audioChunks),
		))))
	src := filepath.Join(t.TempDir(), "interleaved.mp4")
	data := slices.Concat(ftyp, mp4Box("mdat", mdat), mp4Box("moov", video, sound))
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return src, audio
}

// interleavedM4ASHA256 is the remux output from before reads were bounded
// to contiguous sample runs.
const interleavedM4ASHA256 = "e18fbcdb91669fcf3b8ebb57a85be1c9b894a6f1b2ee81a2d582f682d8f814ba"

// TestInterleavedRemuxReadsAudioOnly checks that remuxing a video file reads
// about the audio bytes, not the video chunks between them, and that the
// output is unchanged, also with a buffer smaller than one sample.
func TestInterleavedRemuxReadsAudioOnly(t *testing.T) {
	src, audio := interleavedMP4(t)
	track, err := parseMP4(src)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, bufSize := range []int{1 << 20, 256} {
		counter := &countingReaderAt{r: f}
		reader := &windowReader{f: counter, t: track, buf: make([]byte, bufSize)}
		out := filepath.Join(t.TempDir(), "out.m4a")
		if err := track.writeM4A(out, reader, 0, len(track.sizes), track.ticks()); err != nil {
			t.Fatal(err)
		}
		data := mustRead(t, out)
		if !bytes.HasSuffix(data, audio) {
			t.Errorf("buf %d: mdat does not carry the audio samples", bufSize)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != interleavedM4ASHA256 {
			t.Errorf("buf %d: output sha256 %s, want %s", bufSize, got, interleavedM4ASHA256)
		}
		t.Logf("buf %d: read %d bytes for %d audio bytes", bufSize, counter.n, len(audio))
		if counter.n > 2*int64(len(audio)) {
			t.Errorf("buf %d: read %d bytes, want at most twice the audio", bufSize, counter.n)
		}
	}
}

func TestHasFormat(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	aiff := write("a.AIF")
	m4a := write("b.m4a")
	write("mixed/c.aifc")
	write("mixed/d.m4a")
	write("plain/e.m4a")
	write("plain/.f.aiff") // hidden: Scan skips it too
	write("plain/sub/g.aiff")

	cases := map[string]bool{
		aiff:                         true,
		m4a:                          false,
		filepath.Join(dir, "mixed"):  true,
		filepath.Join(dir, "plain"):  false,
		filepath.Join(dir, "absent"): false,
	}
	for path, want := range cases {
		if got := HasFormat(path, FormatAIFF); got != want {
			t.Errorf("HasFormat(%s) = %v, want %v", filepath.Base(path), got, want)
		}
	}
}
