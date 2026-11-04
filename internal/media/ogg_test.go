package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- synthetic streams ------------------------------------------------------

const synthPreskip = 312

// oggTestPage builds one page with a valid CRC.
func oggTestPage(flags byte, granule int64, serial, seq uint32, lacing, body []byte) []byte {
	b := []byte("OggS\x00")
	b = append(b, flags)
	b = binary.LittleEndian.AppendUint64(b, uint64(granule))
	b = binary.LittleEndian.AppendUint32(b, serial)
	b = binary.LittleEndian.AppendUint32(b, seq)
	b = append(b, 0, 0, 0, 0, byte(len(lacing)))
	b = append(append(b, lacing...), body...)
	binary.LittleEndian.PutUint32(b[22:], oggCRC(0, b))
	return b
}

// synthOgg returns an Opus (or Vorbis) stream of n audio pages of 0.1 s:
// five 10-byte packets per page, except that a 600-byte packet starts at
// the end of page 5, fills page 6 (granule -1) and ends on page 7. Every
// audio byte holds its page index so tests can trace pages through a cut.
func synthOgg(codec string, n int) (data []byte, preskip, rate int64) {
	const serial = 0x1234
	var seq uint32
	add := func(flags byte, granule int64, lacing, body []byte) {
		data = append(data, oggTestPage(flags, granule, serial, seq, lacing, body)...)
		seq++
	}
	if codec == "opus" {
		head := []byte("OpusHead\x01\x01")
		head = binary.LittleEndian.AppendUint16(head, synthPreskip)
		head = append(head, 0x80, 0xBB, 0, 0, 0, 0, 0) // 48000 Hz, gain 0, mapping 0
		add(oggBOS, 0, []byte{byte(len(head))}, head)
		tags := []byte("OpusTags\x00\x00\x00\x00\x00\x00\x00\x00")
		add(0, 0, []byte{byte(len(tags))}, tags)
		preskip, rate = synthPreskip, 48000
	} else {
		id := []byte("\x01vorbis\x00\x00\x00\x00\x01")
		id = binary.LittleEndian.AppendUint32(id, 44100)
		id = append(id, make([]byte, 14)...)
		add(oggBOS, 0, []byte{byte(len(id))}, id)
		comment, setup := []byte("\x03vorbis\x00\x00\x00\x00\x00\x00\x00\x00\x01"), []byte("\x05vorbis-setup")
		add(0, 0, []byte{byte(len(comment)), byte(len(setup))}, append(comment, setup...))
		rate = 44100
	}
	perPage := rate / 10
	fill := func(k, size int) []byte { return bytes.Repeat([]byte{byte(k)}, size) }
	for k := range n {
		granule := preskip + int64(k+1)*perPage
		switch k {
		case 5: // four packets, then the start of a big one
			add(0, granule, []byte{10, 10, 10, 10, 255}, fill(k, 4*10+255))
		case 6: // only the middle of the big packet: no packet ends here
			add(oggContinued, -1, []byte{255}, fill(k, 255))
		case 7: // the end of the big packet and four more
			add(oggContinued, granule, []byte{90, 10, 10, 10, 10}, fill(k, 90+40))
		default:
			add(0, granule, []byte{10, 10, 10, 10, 10}, fill(k, 50))
		}
	}
	return data, preskip, rate
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readOggPages reads every page of path, failing on any invalid page.
func readOggPages(t *testing.T, path string) []oggPage {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var pages []oggPage
	for {
		p, err := readOggPage(br)
		if err != nil {
			if len(pages) == 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("%s page %d: %v", filepath.Base(path), len(pages), err)
			}
			return pages
		}
		pages = append(pages, p)
	}
}

// ---- tests ------------------------------------------------------------------

func TestOggCRC(t *testing.T) {
	// CRC-32 with poly 0x04C11DB7, init 0, no reflection or final xor
	// ("CRC-32/CKSUM" without its final xor).
	if got := oggCRC(0, []byte("123456789")); got != 0x765E7680^0xFFFFFFFF {
		t.Fatalf("oggCRC = %#x", got)
	}
	data, _, _ := synthOgg("opus", 3)
	if _, ok := parseOggPage(data); !ok {
		t.Fatal("synthetic page rejected")
	}
	data[30] ^= 1
	if _, ok := parseOggPage(data); ok {
		t.Fatal("corrupted page accepted")
	}
}

func TestOggSynthetic(t *testing.T) {
	for _, codec := range []string{"opus", "vorbis"} {
		t.Run(codec, func(t *testing.T) {
			data, preskip, rate := synthOgg(codec, 50)
			src := writeTemp(t, "lecture."+codec, data)
			total, err := oggDuration(src)
			if err != nil {
				t.Fatal(err)
			}
			if !near(total, 5, 1e-9) {
				t.Fatalf("duration = %v, want 5", total)
			}
			orig := readOggPages(t, src)
			origGranule := map[byte]int64{} // page index -> granule
			for _, p := range orig[2:] {
				origGranule[p.body()[0]] = p.granule()
			}

			// fits: published as-is with an .ogg name
			pieces, err := prepareOgg(src, t.TempDir(), 0)
			if err != nil || len(pieces) != 1 || pieces[0].MIME != "audio/ogg" ||
				!strings.HasSuffix(pieces[0].Path, ".001.ogg") || pieces[0].Seconds != total {
				t.Fatalf("no limit: %+v, %v", pieces, err)
			}

			const maxSeconds = 1.25
			pieces, err = prepareOgg(src, t.TempDir(), maxSeconds)
			if err != nil {
				t.Fatal(err)
			}
			if len(pieces) < 4 {
				t.Fatalf("pieces = %d", len(pieces))
			}
			start, sum := preskip, 0.0
			for i, pc := range pieces {
				if pc.Seconds <= 0 || pc.Seconds > maxSeconds+1e-9 {
					t.Errorf("piece %d Seconds = %v", i, pc.Seconds)
				}
				if got, err := oggDuration(pc.Path); err != nil || !near(got, pc.Seconds, 1e-9) {
					t.Errorf("piece %d duration %v, %v; Seconds %v", i, got, err, pc.Seconds)
				}
				pages := readOggPages(t, pc.Path)
				if !bytes.Equal(pages[0].raw, orig[0].raw) || !bytes.Equal(pages[1].raw, orig[1].raw) {
					t.Errorf("piece %d: header pages differ", i)
				}
				if pages[2].flags()&oggContinued != 0 {
					t.Errorf("piece %d starts inside a packet", i)
				}
				for j, p := range pages {
					seq := binary.LittleEndian.Uint32(p.raw[18:])
					if seq != uint32(j) || (p.flags()&oggBOS != 0) != (j == 0) ||
						(p.flags()&oggEOS != 0) != (j == len(pages)-1) {
						t.Errorf("piece %d page %d: seq %d flags %#x", i, j, seq, p.flags())
					}
					if j < 2 {
						continue
					}
					g, want := p.granule(), origGranule[p.body()[0]]
					if want != -1 {
						want = want - start + preskip
					}
					if g != want {
						t.Errorf("piece %d page %d: granule %d, want %d", i, j, g, want)
					}
				}
				start += int64(pc.Seconds*float64(rate) + 0.5)
				sum += pc.Seconds
			}
			if !near(sum, total, 1e-9) {
				t.Errorf("sum %v, want %v", sum, total)
			}
		})
	}
}

func TestOggBad(t *testing.T) {
	good, _, _ := synthOgg("opus", 20)
	page := func(i int) (off, size int) {
		for k := 0; ; k++ {
			size = oggPageSize(good[off:])
			if k == i {
				return off, size
			}
			off += size
		}
	}
	mutate := func(fn func(b []byte) []byte) []byte { return fn(bytes.Clone(good)) }
	h1, h1n := page(1)
	a5, _ := page(10)
	cases := []struct {
		name     string
		data     []byte
		duration error // nil: duration must succeed
		prepare  error
	}{
		{"not ogg", []byte("RIFF....WAVEfmt nothing here at all, really nothing"), errOgg, errOgg},
		{"empty", nil, errOgg, errOgg},
		{"cut in header", good[:h1+h1n/2], errOgg, errOgg},
		{"other codec", append(oggTestPage(oggBOS, 0, 1, 0, []byte{4}, []byte("fLaC")), good[h1:]...), errOggCodec, errOggCodec},
		{"crc in audio", mutate(func(b []byte) []byte { b[a5+30] ^= 0xFF; return b }), nil, errOgg},
		{"cut in audio", good[:len(good)-7], nil, nil},
		{"chained", append(bytes.Clone(good[:a5]), append(oggTestPage(oggBOS, 0, 9, 0, []byte{19}, good[28:47]), good[a5:]...)...), nil, errOggCodec},
		{"other serial at end", append(bytes.Clone(good), oggTestPage(0, 99999, 9, 0, []byte{1}, []byte{0})...), errOggCodec, errOggCodec},
		{"audio in header page", append(append(bytes.Clone(good[:h1]),
			oggTestPage(0, 0, 0x1234, 1, []byte{16, 10}, append(bytes.Clone(good[h1+28:h1+h1n]), make([]byte, 10)...))...),
			good[h1+h1n:]...), errOgg, errOgg},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := writeTemp(t, "x.ogg", c.data)
			if _, err := oggDuration(src); !errors.Is(err, c.duration) && (c.duration != nil || err != nil) {
				t.Errorf("duration err = %v, want %v", err, c.duration)
			}
			_, err := prepareOgg(src, t.TempDir(), 0.5)
			if !errors.Is(err, c.prepare) && (c.prepare != nil || err != nil) {
				t.Errorf("prepare err = %v, want %v", err, c.prepare)
			}
		})
	}

	// random damage must never panic
	rng := rand.New(rand.NewSource(1))
	dir := t.TempDir()
	for i := range 300 {
		b := bytes.Clone(good[:rng.Intn(len(good)+1)])
		for range rng.Intn(4) {
			if len(b) > 0 {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
		}
		src := writeTemp(t, "r.ogg", b)
		oggDuration(src)
		prepareOgg(src, filepath.Join(dir, strconv.Itoa(i)), 0.3)
	}
}

// ---- real files (ffmpeg) ------------------------------------------------------

func ffprobeDuration(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v\n%s", filepath.Base(path), err, out)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("ffprobe %s: %q", filepath.Base(path), out)
	}
	return v
}

func TestOggFFmpeg(t *testing.T) {
	needTools(t, "ffmpeg", "ffprobe")
	cases := []struct{ name, ext string }{
		{"libopus", ".ogg"}, {"libopus", ".opus"}, {"vorbis", ".ogg"},
	}
	for _, c := range cases {
		t.Run(c.name+c.ext, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "lecture"+c.ext)
			args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=f=440:d=30", "-c:a", c.name}
			if c.name == "vorbis" { // ffmpeg's own encoder is experimental and stereo only
				args = append(args, "-strict", "-2", "-ac", "2")
			}
			if out, err := exec.Command("ffmpeg", append(args, src)...).CombinedOutput(); err != nil {
				t.Skipf("%s 인코더 없음: %s", c.name, out)
			}
			total, err := oggDuration(src)
			if err != nil {
				t.Fatal(err)
			}
			if want := ffprobeDuration(t, src); !near(total, want, 0.03) {
				t.Fatalf("duration %.3f, ffprobe %.3f", total, want)
			}
			pieces, err := prepareOgg(src, t.TempDir(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if len(pieces) < 4 {
				t.Fatalf("pieces = %d", len(pieces))
			}
			sum := 0.0
			for i, p := range pieces {
				if p.MIME != "audio/ogg" || filepath.Ext(p.Path) != ".ogg" || p.Seconds <= 0 || p.Seconds > 7 {
					t.Errorf("piece %d: %+v", i, p)
				}
				if out, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", p.Path, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
					t.Errorf("piece %d decode: %v\n%s", i, err, out)
				}
				if got := ffprobeDuration(t, p.Path); !near(got, p.Seconds, 0.05) {
					t.Errorf("piece %d: ffprobe %.3f vs Seconds %.3f", i, got, p.Seconds)
				}
				sum += p.Seconds
			}
			if !near(sum, total, 0.01) {
				t.Errorf("sum %.3f, want %.3f", sum, total)
			}
		})
	}
}
