package media

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// fragment rewrites a plain single-track MP4 as a fragmented one: an empty
// sample table plus mvex in moov, then one moof+mdat per `per` samples.
// explicit selects the other common tfhd layout: an absolute base data
// offset and a default sample duration instead of per-sample durations and
// default-base-is-moof.
func fragment(t *testing.T, src string, per int, explicit bool) []byte {
	t.Helper()
	tr, err := parseMP4(src)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := &windowReader{f: f, t: tr, buf: make([]byte, 1<<20)}

	moov := tr.m4aMoov(0, 0, 0, 0)
	trex := fullBox("trex", 0, 0, be(nil).u32(1).u32(1).u32(0).u32(0).u32(0))
	out := slices.Concat(mp4Box("ftyp", []byte("iso6\x00\x00\x00\x00iso6mp41")), mp4Box("moov", moov[8:], mp4Box("mvex", trex)))
	for seq, from := 1, 0; from < len(tr.sizes); seq, from = seq+1, from+per {
		to := min(from+per, len(tr.sizes))
		var payload []byte
		for i := from; i < to; i++ {
			data, err := reader.sample(i)
			if err != nil {
				t.Fatal(err)
			}
			payload = append(payload, data...)
		}
		build := func(dataOffset uint32, base uint64) []byte {
			tfhd := fullBox("tfhd", 0, 0x20000, be(nil).u32(1))
			trunFlags := uint32(0x1 | 0x100 | 0x200)
			if explicit {
				tfhd = fullBox("tfhd", 0, 0x1|0x8, be(nil).u32(1).u64(base).u32(tr.deltas[from]))
				trunFlags = 0x1 | 0x200
			}
			run := be(nil).u32(uint32(to - from)).u32(dataOffset)
			for i := from; i < to; i++ {
				if trunFlags&0x100 != 0 {
					run = run.u32(tr.deltas[i])
				}
				run = run.u32(tr.sizes[i])
			}
			return mp4Box("moof",
				fullBox("mfhd", 0, 0, be(nil).u32(uint32(seq))),
				mp4Box("traf", tfhd, fullBox("trun", 0, trunFlags, run)))
		}
		moofLen := len(build(0, 0))
		var moof []byte
		if explicit { // offsets relative to the start of the moof, given absolutely
			moof = build(uint32(moofLen+8), uint64(len(out)))
		} else {
			moof = build(uint32(moofLen+8), 0)
		}
		out = slices.Concat(out, moof, mp4Box("mdat", payload))
	}
	return out
}

func TestFragmentedMP4Remux(t *testing.T) {
	fx := fixtures(t)
	orig := filepath.Join(fx, "x.m4a")
	want, err := parseMP4(orig)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		explicit bool
	}{{"base-is-moof", false}, {"explicit-base", true}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "frag.m4a")
			os.WriteFile(src, fragment(t, orig, 43, c.explicit), 0o644)
			got, err := parseMP4(src)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.sizes, want.sizes) || !slices.Equal(got.deltas, want.deltas) {
				t.Fatalf("fragment tables differ: %d samples, want %d", len(got.sizes), len(want.sizes))
			}
			if !near(got.seconds(), want.seconds(), 1e-9) {
				t.Errorf("seconds = %v, want %v", got.seconds(), want.seconds())
			}
			// Unsplit, the remux equals the one from the plain file.
			a, err := Prepare(Track{Path: src, Format: FormatAACMP4}, filepath.Join(dir, "a"), 0)
			if err != nil {
				t.Fatal(err)
			}
			b, err := Prepare(Track{Path: orig, Format: FormatAACMP4}, filepath.Join(dir, "b"), 0)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(mustRead(t, a[0].Path), mustRead(t, b[0].Path)) {
				t.Error("remux of the fragmented file differs from the plain file's")
			}
			pieces, err := Prepare(Track{Path: src, Format: FormatAACMP4}, filepath.Join(dir, "c"), 2)
			if err != nil {
				t.Fatal(err)
			}
			checkPieces(t, pieces, got.seconds(), 2, "audio/m4a")
		})
	}
}

// OBS's hybrid MP4 leaves the recording's moof boxes next to a complete
// final moov without mvex; the moofs must be ignored, not added twice.
func TestHybridMP4IgnoresMoof(t *testing.T) {
	fx := fixtures(t)
	orig := filepath.Join(fx, "x.m4a")
	src := filepath.Join(t.TempDir(), "hybrid.m4a")
	stray := mp4Box("moof", fullBox("mfhd", 0, 0, be(nil).u32(1)),
		mp4Box("traf", fullBox("tfhd", 0, 0x20000, be(nil).u32(1)), fullBox("trun", 0, 0x200, be(nil).u32(1).u32(100))))
	os.WriteFile(src, slices.Concat(mustRead(t, orig), stray), 0o644)
	got, err := parseMP4(src)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := parseMP4(orig)
	if len(got.sizes) != len(want.sizes) {
		t.Errorf("samples = %d, want %d", len(got.sizes), len(want.sizes))
	}
}

// Real fragmented files from ffmpeg's muxer flags (CI has no ffmpeg).
func TestFragmentedMP4FFmpeg(t *testing.T) {
	needTools(t, "ffmpeg", "ffprobe")
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	ffmpeg := func(args ...string) {
		t.Helper()
		args = append([]string{"-hide_banner", "-v", "error", "-y"}, args...)
		if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	ffmpeg("-f", "lavfi", "-i", "sine=f=440:d=12", "-f", "lavfi", "-i", "testsrc=d=12:s=160x120:r=10",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src)
	for name, flags := range map[string]string{
		"empty_moov": "frag_keyframe+empty_moov",
		"cmaf":       "frag_keyframe+empty_moov+default_base_moof",
		"separate":   "frag_keyframe+empty_moov+default_base_moof+separate_moof",
		"moov_first": "frag_keyframe",
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".mp4")
			ffmpeg("-i", src, "-c", "copy", "-movflags", flags, p)
			total, err := Duration(p)
			if err != nil {
				t.Fatal(err)
			}
			if !near(total, 12, 0.3) {
				t.Errorf("Duration = %v", total)
			}
			pieces, err := Prepare(Track{Path: p, Format: FormatAACMP4}, filepath.Join(dir, "out-"+name), 5)
			if err != nil {
				t.Fatal(err)
			}
			sum := 0.0
			for _, pc := range pieces {
				if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", pc.Path, "-f", "null", "-").CombinedOutput(); err != nil {
					t.Errorf("%s: decode: %v\n%s", filepath.Base(pc.Path), err, out)
				}
				sum += pc.Seconds
			}
			if len(pieces) < 3 || !near(sum, total, 0.1) {
				t.Errorf("pieces = %d, sum %v, total %v", len(pieces), sum, total)
			}
		})
	}
}

// MP3 audio in an MP4 comes out as the plain MP3 frames, byte for byte, in
// both the ISO (esds objectType 0x6B) and QuickTime (".mp3") sample entries.
func TestMP3InMP4(t *testing.T) {
	const frames = 400
	raw := silentMP3(frames, false, false)
	var sizes []uint32
	var offsets []int64
	raw = raw[:len(raw)-128] // drop the ID3v1 tag: an MP4 holds only frames
	for off := 0; off < len(raw); {
		h, ok := parseMP3Header(raw[off:])
		if !ok {
			t.Fatalf("bad frame at %d", off)
		}
		sizes, offsets = append(sizes, uint32(h.length)), append(offsets, int64(off))
		off += h.length
	}
	sound := be(make([]byte, 6)).u16(1).u32(0).u32(0).u16(2).u16(16).u32(0).u32(44100 << 16)
	decoderConfig := append([]byte{0x6B, 0x15}, make([]byte, 11)...)
	es := slices.Concat([]byte{0, 0, 0, 0x04, byte(len(decoderConfig))}, decoderConfig)
	esds := fullBox("esds", 0, 0, append([]byte{0x03, byte(len(es))}, es...))
	for name, entry := range map[string][]byte{
		"esds":      mp4Box("mp4a", sound, esds),
		"quicktime": mp4Box(".mp3", sound),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			rawPath := filepath.Join(dir, "raw.mp3")
			os.WriteFile(rawPath, raw, 0o644)
			f, err := os.Open(rawPath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			deltas := make([]uint32, len(sizes))
			for i := range deltas {
				deltas[i] = 1152
			}
			tr := &mp4Track{timescale: 44100, sampleEntry: entry, sizes: sizes, offsets: offsets, deltas: deltas}
			src := filepath.Join(dir, "x.mp4")
			if err := tr.writeM4A(src, &windowReader{f: f, t: tr, buf: make([]byte, 1<<20)}, 0, len(sizes), tr.ticks()); err != nil {
				t.Fatal(err)
			}
			got, err := Duration(src)
			if err != nil {
				t.Fatal(err)
			}
			if want := float64(frames*1152) / 44100; !near(got, want, 1e-6) {
				t.Errorf("Duration = %v, want %v", got, want)
			}
			pieces, err := Prepare(Track{Path: src, Format: FormatAACMP4}, filepath.Join(dir, "out"), 2)
			if err != nil {
				t.Fatal(err)
			}
			var joined []byte
			for _, p := range pieces {
				if p.MIME != "audio/mp3" || filepath.Ext(p.Path) != ".mp3" || p.Seconds > 2 {
					t.Errorf("piece %+v", p)
				}
				joined = append(joined, mustRead(t, p.Path)...)
			}
			if len(pieces) != 6 || !bytes.Equal(joined, raw) {
				t.Errorf("pieces = %d, bytes equal = %v", len(pieces), bytes.Equal(joined, raw))
			}
		})
	}
}
