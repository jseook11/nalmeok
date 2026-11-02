package media

import (
	"bytes"
	"encoding/binary"
	"errors"
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
)

// ---- fixtures ---------------------------------------------------------------

var (
	fixtureOnce sync.Once
	fixtureDir  string
	fixtureErr  error
)

const sayText = "안녕하세요 테스트입니다. 오늘은 날먹 앱의 미디어 패키지를 시험합니다. 이 문장은 충분히 길어야 여러 조각으로 나눌 수 있습니다."

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
	needTools(t, "say", "afconvert", "afinfo")
	fixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nalmeok-media-")
		if err != nil {
			fixtureErr = err
			return
		}
		fixtureDir = dir
		aiff := filepath.Join(dir, "x.aiff")
		cmds := [][]string{
			{"say", "-o", aiff, sayText},
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

var (
	afinfoDurationRE = regexp.MustCompile(`estimated duration: ([0-9.]+) sec`)
	afinfoPacketsRE  = regexp.MustCompile(`audio packets: ([0-9]+)`)
	afinfoRateRE     = regexp.MustCompile(`Data format:\s+\d+ ch,\s+(\d+) Hz`)
)

// afinfoDuration runs afinfo (which must succeed). For ADTS, afinfo's
// "estimated duration" is a bitrate guess even for Apple's own files, so the
// length is taken from the parsed packet count instead.
func afinfoDuration(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("afinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("afinfo %s: %v\n%s", filepath.Base(path), err, out)
	}
	if strings.HasSuffix(path, ".aac") {
		pm, rm := afinfoPacketsRE.FindSubmatch(out), afinfoRateRE.FindSubmatch(out)
		if pm == nil || rm == nil {
			t.Fatalf("afinfo %s: packets 없음\n%s", filepath.Base(path), out)
		}
		packets, _ := strconv.Atoi(string(pm[1]))
		rate, _ := strconv.Atoi(string(rm[1]))
		return float64(packets*1024) / float64(rate)
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
		{"empty", nil, "오디오 파일이 없습니다"},
		{"only pdf and text", []string{"a.pdf", "note.txt"}, "오디오 파일이 없습니다"},
		{"eleven", []string{"1.mp3", "2.mp3", "3.mp3", "4.mp3", "5.mp3", "6.mp3", "7.mp3", "8.mp3", "9.mp3", "10.mp3", "11.mp3"}, "1~10개"},
		{"two pdfs", []string{"1.mp3", "a.pdf", "b.PDF"}, "PDF는 폴더에 최대 1개"},
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
	if in.PDF != filepath.Join(dir, "용어.pdf") {
		t.Errorf("PDF = %q", in.PDF)
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
	if file.Kind != "file" || file.Name != "10강" || len(file.Tracks) != 1 || file.PDF != "" {
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
	if !strings.Contains(err.Error(), "AAC 오디오가 아닌 MP4는 지원하지 않습니다") {
		t.Errorf("message = %q", err)
	}
}

func mp4Box(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(b)))
	return append(append(out, typ...), b...)
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
		if _, err := Duration(p); err == nil || !strings.Contains(err.Error(), "지원하지 않는 MP4 형식") {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := Prepare(Track{Path: p, Format: FormatAACMP4}, dir, 2); err == nil {
			t.Errorf("%s: Prepare should fail", name)
		}
	}
}

// adtsFrames walks an ADTS file and returns the frame count, checking every
// header against the expected fields.
func adtsFrames(t *testing.T, path string, wantProfile, wantSR, wantCh int) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := 0
	for off := 0; off < len(data); {
		h := data[off:]
		if len(h) < 7 || h[0] != 0xFF || h[1]&0xF6 != 0xF0 {
			t.Fatalf("%s: frame %d: bad sync at %d", filepath.Base(path), frames, off)
		}
		profile := int(h[2] >> 6)
		sr := int(h[2]>>2) & 0xF
		ch := int(h[2]&1)<<2 | int(h[3]>>6)
		length := int(h[3]&3)<<11 | int(h[4])<<3 | int(h[5]>>5)
		if profile != wantProfile || sr != wantSR || ch != wantCh {
			t.Fatalf("frame %d: profile %d sr %d ch %d", frames, profile, sr, ch)
		}
		if length < 7 || off+length > len(data) {
			t.Fatalf("frame %d: length %d overflows", frames, length)
		}
		off += length
		frames++
	}
	return frames
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
			checkPieces(t, pieces, total, 2, "audio/aac")
			frames := 0
			for _, p := range pieces {
				if filepath.Ext(p.Path) != ".aac" || filepath.Dir(p.Path) != dir {
					t.Errorf("path = %s", p.Path)
				}
				// say → 22050 Hz mono AAC-LC: profile 1, index 7, 1 channel
				frames += adtsFrames(t, p.Path, 1, 7, 1)
			}
			if frames != len(track.sizes) {
				t.Errorf("ADTS frames %d, samples %d", frames, len(track.sizes))
			}

			whole, err := Prepare(Track{Path: src, Format: FormatAACMP4}, t.TempDir(), 0)
			if err != nil || len(whole) != 1 || !near(whole[0].Seconds, total, 1e-9) {
				t.Fatalf("unlimited Prepare = %+v, %v", whole, err)
			}
		})
	}
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

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
