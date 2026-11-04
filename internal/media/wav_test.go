package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// pcmWAVBytes builds a mono 16-bit 8 kHz PCM WAV of the given length.
func pcmWAVBytes(seconds int) []byte {
	const rate, blockAlign = 8000, 2
	data := make([]byte, seconds*rate*blockAlign)
	for i := range data {
		data[i] = byte(i * 7)
	}
	b := make([]byte, 44, 44+len(data))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(data)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*blockAlign)
	binary.LittleEndian.PutUint16(b[32:], blockAlign)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(data)))
	return append(b, data...)
}

// rf64Bytes rewrites a plain WAV as RF64/BW64: a ds64 chunk carries the real
// sizes and the data chunk's 32-bit size is 0xFFFFFFFF.
func rf64Bytes(wav []byte, form string) []byte {
	var fmtChunk, pcm []byte
	for p := 12; p+8 <= len(wav); {
		id := string(wav[p : p+4])
		n := int(binary.LittleEndian.Uint32(wav[p+4:]))
		switch id {
		case "fmt ":
			fmtChunk = wav[p : p+8+n]
		case "data":
			pcm = wav[p+8 : p+8+n]
		}
		p += 8 + n + n&1
	}
	blockAlign := int(binary.LittleEndian.Uint16(fmtChunk[8+12:]))
	ds64 := make([]byte, 28)
	binary.LittleEndian.PutUint64(ds64[8:], uint64(len(pcm)))
	binary.LittleEndian.PutUint64(ds64[16:], uint64(len(pcm)/blockAlign))
	out := []byte(form)
	out = binary.LittleEndian.AppendUint32(out, 0xFFFFFFFF) // RF64 sentinel
	out = append(out, "WAVEds64"...)
	out = binary.LittleEndian.AppendUint32(out, 28)
	out = append(out, ds64...)
	out = append(out, fmtChunk...)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, 0xFFFFFFFF)
	out = append(out, pcm...)
	binary.LittleEndian.PutUint64(out[20:], uint64(len(out)-8)) // ds64 riffSize
	return out
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.wav")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func haveTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestRF64Duration(t *testing.T) {
	for _, form := range []string{"RF64", "BW64"} {
		t.Run(form, func(t *testing.T) {
			path := writeTemp(t, rf64Bytes(pcmWAVBytes(3), form))
			w, err := parseWAV(path)
			if err != nil {
				t.Fatal(err)
			}
			if !w.rf64 {
				t.Error("source should be marked RF64")
			}
			got, err := Duration(path)
			if err != nil {
				t.Fatal(err)
			}
			if !near(got, 3, 1e-9) {
				t.Errorf("Duration = %v, want 3", got)
			}
			if haveTool("afinfo") {
				if want := afinfoDuration(t, path); !near(got, want, 0.01) {
					t.Errorf("Duration = %v, afinfo = %v", got, want)
				}
			}
		})
	}
}

func TestPrepareRF64(t *testing.T) {
	src := writeTemp(t, rf64Bytes(pcmWAVBytes(3), "RF64"))
	for _, tc := range []struct {
		maxSeconds float64
		want       int
	}{{0, 1}, {2, 2}} {
		pieces, err := Prepare(Track{Path: src, Format: FormatWAV}, t.TempDir(), tc.maxSeconds)
		if err != nil {
			t.Fatal(err)
		}
		if len(pieces) != tc.want {
			t.Fatalf("maxSeconds %v: %d pieces, want %d", tc.maxSeconds, len(pieces), tc.want)
		}
		sum := 0.0
		for i, p := range pieces {
			data, err := os.ReadFile(p.Path)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
				t.Errorf("piece %d is not a plain RIFF WAV: %q", i, data[:min(len(data), 12)])
			}
			if pc, _ := os.ReadFile(src); bytes.Equal(data, pc) {
				t.Errorf("piece %d is the RF64 source itself", i)
			}
			pi, err := parseWAV(p.Path)
			if err != nil {
				t.Errorf("piece %d: %v", i, err)
				continue
			}
			if pi.rf64 || !near(pi.seconds(), p.Seconds, 1e-9) {
				t.Errorf("piece %d: rf64=%v parsed %v Seconds %v", i, pi.rf64, pi.seconds(), p.Seconds)
			}
			if haveTool("afinfo") {
				if got := afinfoDuration(t, p.Path); !near(got, p.Seconds, 1e-9) {
					t.Errorf("piece %d: afinfo %v, Seconds %v", i, got, p.Seconds)
				}
			}
			sum += p.Seconds
		}
		if !near(sum, 3, 1e-9) {
			t.Errorf("maxSeconds %v: sum %v, want 3", tc.maxSeconds, sum)
		}
	}
}

func TestRF64MissingDs64(t *testing.T) {
	wav := pcmWAVBytes(1)
	// RF64 marker with a 0xFFFFFFFF data size but no ds64 chunk.
	out := []byte("RF64")
	out = binary.LittleEndian.AppendUint32(out, 0xFFFFFFFF)
	out = append(out, "WAVE"...)
	out = append(out, wav[12:36]...) // fmt chunk
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, 0xFFFFFFFF)
	out = append(out, wav[44:]...)
	if _, err := Duration(writeTemp(t, out)); !errors.Is(err, errWAV) {
		t.Fatalf("err = %v, want errWAV", err)
	}
}
