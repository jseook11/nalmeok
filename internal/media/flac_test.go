package media

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// flacFixture encodes the shared AIFF fixture to FLAC with afconvert.
func flacFixture(t *testing.T) string {
	t.Helper()
	fx := fixtures(t)
	out := filepath.Join(t.TempDir(), "x.flac")
	if b, err := exec.Command("afconvert", "-f", "flac", "-d", "flac", filepath.Join(fx, "x.aiff"), out).CombinedOutput(); err != nil {
		t.Fatalf("afconvert flac: %v\n%s", err, b)
	}
	return out
}

func flacAudioOff(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := parseFLAC(f)
	if err != nil {
		t.Fatal(err)
	}
	return fi.audioOff
}

// withUnknownTotal writes a copy of src whose STREAMINFO total is 0, as
// streaming encoders leave it.
func withUnknownTotal(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	si := data[8:] // "fLaC" + block header
	si[13] &= 0xF0
	clear(si[14:18])
	dst := filepath.Join(t.TempDir(), "unknown.flac")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestFLACDuration(t *testing.T) {
	src := flacFixture(t)
	want := afinfoDuration(t, src)
	got, err := Duration(src)
	if err != nil {
		t.Fatal(err)
	}
	if !near(got, want, 0.01) {
		t.Errorf("Duration %.3f, afinfo %.3f", got, want)
	}
	if got, err := Duration(withUnknownTotal(t, src)); err != nil || !near(got, want, 0.01) {
		t.Errorf("unknown total: Duration %.3f, %v; want %.3f", got, err, want)
	}

	// a leading ID3v2 tag is skipped
	data, _ := os.ReadFile(src)
	tag := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x05"), "hello"...)
	tagged := filepath.Join(t.TempDir(), "tagged.flac")
	os.WriteFile(tagged, append(tag, data...), 0o644)
	if got, err := Duration(tagged); err != nil || !near(got, want, 0.01) {
		t.Errorf("ID3 tagged: Duration %.3f, %v", got, err)
	}
}

func TestPrepareFLAC(t *testing.T) {
	src := flacFixture(t)
	total, err := Duration(src)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(src)
	frames := data[flacAudioOff(t, src):]

	for name, path := range map[string]string{"known": src, "unknown total": withUnknownTotal(t, src)} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			pieces, err := Prepare(Track{Path: path, Format: FormatFLAC}, dir, 2)
			if err != nil {
				t.Fatal(err)
			}
			checkPieces(t, pieces, total, 2, "audio/flac")
			var joined []byte
			for i, p := range pieces {
				d, _ := Duration(p.Path) // STREAMINFO total of the piece
				if !near(d, p.Seconds, 1e-9) {
					t.Errorf("piece %d: STREAMINFO %.3f vs Seconds %.3f", i, d, p.Seconds)
				}
				wav := filepath.Join(dir, "decoded.wav")
				if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", p.Path, wav).CombinedOutput(); err != nil {
					t.Fatalf("piece %d decode: %v\n%s", i, err, out)
				}
				if got := afinfoDuration(t, wav); !near(got, p.Seconds, 0.01) {
					t.Errorf("piece %d decoded %.3f vs Seconds %.3f", i, got, p.Seconds)
				}
				b, _ := os.ReadFile(p.Path)
				joined = append(joined, b[flacAudioOff(t, p.Path):]...)
			}
			if !bytes.Equal(joined, frames) {
				t.Errorf("pieces' frames (%d bytes) differ from the source's (%d bytes)", len(joined), len(frames))
			}
			// later pieces start at a frame number other than 0
			t.Run("ffmpeg", func(t *testing.T) {
				needTools(t, "ffmpeg", "ffprobe")
				for i, p := range pieces {
					if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", p.Path, "-f", "null", "-").CombinedOutput(); err != nil {
						t.Errorf("piece %d: ffmpeg: %v\n%s", i, err, out)
					}
				}
			})
		})
	}

	for _, maxSeconds := range []float64{0, total + 1} {
		pieces, err := Prepare(Track{Path: src, Format: FormatFLAC}, t.TempDir(), maxSeconds)
		if err != nil || len(pieces) != 1 || pieces[0].MIME != "audio/flac" || !near(pieces[0].Seconds, total, 1e-9) {
			t.Fatalf("max %v: %+v, %v", maxSeconds, pieces, err)
		}
		if b, _ := os.ReadFile(pieces[0].Path); !bytes.Equal(b, data) {
			t.Errorf("max %v: whole piece differs from the source", maxSeconds)
		}
	}
}

// flacFrames lists the frames of a FLAC file.
func flacFrames(t *testing.T, path string) []flacFrame {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := parseFLAC(f)
	if err != nil {
		t.Fatal(err)
	}
	var frames []flacFrame
	if err := walkFLAC(f, fi, func(fr flacFrame) error { frames = append(frames, fr); return nil }); err != nil {
		t.Fatal(err)
	}
	return frames
}

// Real files may end in an ID3v1 tag or, when the recorder died, in a
// cut-off frame. Both are left out; the frames before them are kept.
func TestFLACTail(t *testing.T) {
	src := withUnknownTotal(t, flacFixture(t))
	data, _ := os.ReadFile(src)
	frames := flacFrames(t, src)
	last := frames[len(frames)-1]
	if last.size <= 100 {
		t.Fatalf("last frame only %d bytes", last.size)
	}
	var total int64
	for _, fr := range frames {
		total += fr.samples
	}
	whole, err := Duration(src)
	if err != nil {
		t.Fatalf("Duration %.3f, %v", whole, err)
	}
	rate := float64(total) / whole
	tag := append([]byte("TAG"), bytes.Repeat([]byte{' '}, 125)...)
	cases := []struct {
		name    string
		data    []byte
		samples int64 // kept
		frames  []byte
	}{
		{"ID3v1", append(bytes.Clone(data), tag...), total, data[frames[0].off:]},
		{"cut off", data[:len(data)-100], total - last.samples, data[frames[0].off:last.off]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "x.flac")
			os.WriteFile(path, c.data, 0o644)
			want := float64(c.samples) / rate
			if got, err := Duration(path); err != nil || !near(got, want, 1e-9) {
				t.Errorf("Duration %.3f, %v; want %.3f", got, err, want)
			}
			pieces, err := prepareFLAC(path, dir, 2)
			if err != nil {
				t.Fatal(err)
			}
			var joined []byte
			sum := 0.0
			for _, p := range pieces {
				b, _ := os.ReadFile(p.Path)
				joined = append(joined, b[flacAudioOff(t, p.Path):]...)
				sum += p.Seconds
			}
			if !bytes.Equal(joined, c.frames) || !near(sum, want, 1e-9) {
				t.Errorf("pieces hold %d bytes, %.3fs; want %d bytes, %.3fs", len(joined), sum, len(c.frames), want)
			}
		})
	}
}

func TestFLACCorrupt(t *testing.T) {
	src := flacFixture(t)
	unknown := withUnknownTotal(t, src)
	data, _ := os.ReadFile(unknown)
	off := flacAudioOff(t, unknown)
	dir := t.TempDir()
	write := func(b []byte) string {
		p := filepath.Join(dir, "bad.flac")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Inputs that must be rejected, by Duration and by a split.
	flipped := bytes.Clone(data)
	flipped[off+(int64(len(data))-off)/2] ^= 0x10
	bad := map[string][]byte{
		"empty":           nil,
		"magic only":      []byte("fLaC"),
		"not flac":        []byte("RIFF....WAVEfmt "),
		"no frames":       data[:off],
		"cut in metadata": data[:20],
		"flipped audio":   flipped,
		// longer than any frame: not a cut-off last frame
		"junk after": append(bytes.Clone(data), bytes.Repeat([]byte("junk"), 1<<18)...),
	}
	for name, b := range bad {
		p := write(b)
		if _, err := Duration(p); !errors.Is(err, errFLAC) {
			t.Errorf("%s: Duration err = %v", name, err)
		}
		if _, err := prepareFLAC(p, dir, 1); !errors.Is(err, errFLAC) {
			t.Errorf("%s: prepare err = %v", name, err)
		}
	}

	// Random damage never panics and fails only with errFLAC.
	out := t.TempDir()
	rng := rand.New(rand.NewSource(1))
	for range 200 {
		b := bytes.Clone(data)
		switch rng.Intn(3) {
		case 0:
			b = b[:rng.Intn(len(b))]
		case 1:
			for range 1 + rng.Intn(4) {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
		default:
			b = b[:rng.Intn(200)]
		}
		p := write(b)
		if _, err := Duration(p); err != nil && !errors.Is(err, errFLAC) {
			t.Fatalf("Duration err = %v", err)
		}
		if _, err := prepareFLAC(p, out, 1); err != nil && !errors.Is(err, errFLAC) {
			t.Fatalf("prepare err = %v", err)
		}
	}
}
