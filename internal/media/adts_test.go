package media

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// adtsFrame builds one ADTS frame (AAC LC unless profile says otherwise)
// around payload, with a (dummy) CRC when crc is set.
func adtsFrame(profile, srIndex, channels, blocks int, crc bool, payload []byte) []byte {
	head := 7
	if crc {
		head = 9
	}
	n := head + len(payload)
	b := []byte{
		0xFF, 0xF1,
		byte(profile<<6 | srIndex<<2 | channels>>2),
		byte(channels&3<<6 | n>>11),
		byte(n >> 3),
		byte(n&7<<5 | 0x1F),
		byte(0xFC | blocks),
	}
	if crc {
		b[1] = 0xF0
		b = append(b, 0xAB, 0xCD)
	}
	return append(b, payload...)
}

// synthADTS returns a 48 kHz stereo stream of frames with distinct payloads.
func synthADTS(frames int, crc bool) ([]byte, [][]byte) {
	var data []byte
	var payloads [][]byte
	for i := range frames {
		p := bytes.Repeat([]byte{byte(i)}, 100+i%50)
		payloads = append(payloads, p)
		data = append(data, adtsFrame(1, 3, 2, 0, crc, p)...)
	}
	return data, payloads
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// payloads returns the samples of t read from path, in order.
func trackPayloads(t *testing.T, tr *mp4Track, path string) []byte {
	t.Helper()
	data := mustRead(t, path)
	var out []byte
	for i := range tr.sizes {
		out = append(out, data[tr.offsets[i]:tr.offsets[i]+int64(tr.sizes[i])]...)
	}
	return out
}

func TestADTSSynthetic(t *testing.T) {
	for _, crc := range []bool{false, true} {
		data, payloads := synthADTS(300, crc)
		id3 := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x10"), make([]byte, 16)...)
		src := writeTemp(t, "x.aac", append(id3, data...))
		tr, err := parseADTS(src)
		if err != nil {
			t.Fatalf("crc=%v: %v", crc, err)
		}
		if len(tr.sizes) != 300 || tr.timescale != 48000 || !near(tr.seconds(), 300*1024/48000.0, 1e-9) {
			t.Fatalf("crc=%v: %d frames, timescale %d, %v s", crc, len(tr.sizes), tr.timescale, tr.seconds())
		}
		if !bytes.Equal(trackPayloads(t, tr, src), bytes.Join(payloads, nil)) {
			t.Errorf("crc=%v: payloads differ", crc)
		}
		// The built sample entry must read back through the MP4 parser.
		boxes, err := childBoxes(tr.sampleEntry)
		if err != nil || len(boxes) != 1 || boxes[0].typ != "mp4a" {
			t.Fatalf("sample entry: %v %v", boxes, err)
		}
		back := &mp4Track{}
		if err := back.parseMP4A(boxes[0].body); err != nil {
			t.Fatal(err)
		}
		if back.profile != 1 || back.srIndex != 3 || back.channels != 2 {
			t.Errorf("ASC read back as %+v", back)
		}

		pieces, err := prepareADTS(src, t.TempDir(), 2)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		sum := 0.0
		for _, p := range pieces {
			pt, err := parseMP4(p.Path)
			if err != nil {
				t.Fatal(err)
			}
			if p.Seconds > 2 || !near(pt.seconds(), p.Seconds, 1e-9) {
				t.Errorf("piece %s: Seconds %v, coded %v", p.Path, p.Seconds, pt.seconds())
			}
			sum += p.Seconds
			got = append(got, trackPayloads(t, pt, p.Path)...)
		}
		if len(pieces) != 4 || !near(sum, tr.seconds(), 1e-9) || !bytes.Equal(got, bytes.Join(payloads, nil)) {
			t.Errorf("crc=%v: %d pieces, %v s, payloads equal %v", crc, len(pieces), sum, bytes.Equal(got, bytes.Join(payloads, nil)))
		}
	}
}

func TestADTSMalformed(t *testing.T) {
	data, _ := synthADTS(20, false)
	start := func(k int) int { // offset of frame k (see synthADTS)
		n := 0
		for i := range k {
			n += 7 + 100 + i%50
		}
		return n
	}
	frame := start(1)
	broken := bytes.Clone(data)
	broken[start(5)] = 0 // sixth header loses its sync
	mixed := append(bytes.Clone(data[:start(3)]), adtsFrame(1, 4, 2, 0, false, make([]byte, 80))...)
	mixed = append(mixed, data[start(3):]...)

	bad := map[string][]byte{
		"empty":          nil,
		"garbage":        bytes.Repeat([]byte("not adts "), 50),
		"header only":    data[:7],
		"cut first":      data[:frame-1],
		"broken middle":  broken,
		"stream changes": mixed,
		"id3 only":       []byte("ID3\x03\x00\x00\x00\x00\x00\x00"),
	}
	for name, b := range bad {
		if _, err := parseADTS(writeTemp(t, "x.aac", b)); !errors.Is(err, errADTS) {
			t.Errorf("%s: err = %v, want errADTS", name, err)
		}
	}

	unsupported := map[string][]byte{
		"channel config 0": adtsFrame(1, 3, 0, 0, false, make([]byte, 50)),
		"two raw blocks":   append(bytes.Clone(data), adtsFrame(1, 3, 2, 1, false, make([]byte, 50))...),
	}
	for name, b := range unsupported {
		if _, err := parseADTS(writeTemp(t, "x.aac", b)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}

	// Junk after the last full frame is ignored.
	tag := append([]byte("TAG"), bytes.Repeat([]byte{0xFF}, 125)...)
	for name, b := range map[string][]byte{
		"id3v1":     append(bytes.Clone(data), tag...),
		"cut frame": append(bytes.Clone(data), data[:frame/2]...),
	} {
		tr, err := parseADTS(writeTemp(t, "x.aac", b))
		if err != nil || len(tr.sizes) != 20 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPrepareADTS(t *testing.T) {
	fx := fixtures(t)
	src := filepath.Join(t.TempDir(), "x.aac")
	if out, err := exec.Command("afconvert", "-f", "adts", "-d", "aac", filepath.Join(fx, "x.aiff"), src).CombinedOutput(); err != nil {
		t.Fatalf("afconvert adts: %v\n%s", err, out)
	}
	tr, err := parseADTS(src)
	if err != nil {
		t.Fatal(err)
	}
	total, err := Duration(src)
	if err != nil || total != tr.seconds() {
		t.Fatalf("Duration = %v, %v", total, err)
	}
	// afinfo only estimates an ADTS duration from the bit rate, but it counts
	// the packets exactly, and the decoded WAV holds every coded sample.
	out, _ := exec.Command("afinfo", src).CombinedOutput()
	if want := fmt.Sprintf("audio packets: %d\n", len(tr.sizes)); !bytes.Contains(out, []byte(want)) {
		t.Errorf("afinfo disagrees with %d frames:\n%s", len(tr.sizes), out)
	}
	decoded := filepath.Join(t.TempDir(), "x.wav")
	if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", src, decoded).CombinedOutput(); err != nil {
		t.Fatalf("afconvert decode: %v\n%s", err, out)
	}
	if got := afinfoDuration(t, decoded); !near(got, total, 0.01) {
		t.Errorf("Duration %.3f, decoded %.3f", total, got)
	}

	pieces, err := Prepare(Track{Path: src, Format: FormatADTS}, t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	checkPieces(t, pieces, total, 2, "audio/m4a")
	var got []byte
	for i, p := range pieces {
		pt, err := parseMP4(p.Path)
		if err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		if !bytes.Equal(pt.sampleEntry, tr.sampleEntry) {
			t.Errorf("piece %d: sample entry differs", i)
		}
		got = append(got, trackPayloads(t, pt, p.Path)...)
		// No gapless metadata: afinfo subtracts the standard 2112-sample delay.
		if a := afinfoDuration(t, p.Path); !near(a, p.Seconds-2112/float64(tr.timescale), 0.001) {
			t.Errorf("piece %d: afinfo %.4f, Seconds %.4f", i, a, p.Seconds)
		}
		wav := filepath.Join(t.TempDir(), "out.wav")
		if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", p.Path, wav).CombinedOutput(); err != nil {
			t.Errorf("piece %d: afconvert decode failed: %v\n%s", i, err, out)
		}
	}
	if !bytes.Equal(got, trackPayloads(t, tr, src)) {
		t.Error("pieces do not carry the source AAC frames in order")
	}

	whole, err := Prepare(Track{Path: src, Format: FormatADTS}, t.TempDir(), 0)
	if err != nil || len(whole) != 1 || filepath.Ext(whole[0].Path) != ".m4a" || whole[0].Seconds != total {
		t.Fatalf("unlimited Prepare = %+v, %v", whole, err)
	}
}
