package media

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// convertAIFF runs afconvert with args and returns the new file path.
func convertAIFF(t *testing.T, src, name string, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := append([]string{"afconvert"}, append(args, src, out)...)
	if b, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", cmd, err, b)
	}
	return out
}

// aiffFixtures converts the shared source into the AIFF/AIFC variants we
// accept.
func aiffFixtures(t *testing.T) map[string]string {
	t.Helper()
	src := filepath.Join(fixtures(t), "source.wav")
	return map[string]string{
		"aiff.aiff": convertAIFF(t, src, "aiff.aiff", "-f", "AIFF", "-d", "BEI16"),
		"aifc.aifc": convertAIFF(t, src, "aifc.aifc", "-f", "AIFC", "-d", "BEI16"),
		"f32.aiff":  convertAIFF(t, src, "f32.aiff", "-f", "AIFC", "-d", "BEF32"),
	}
}

func TestAIFFDuration(t *testing.T) {
	for name, path := range aiffFixtures(t) {
		t.Run(name, func(t *testing.T) {
			got, err := Duration(path)
			if err != nil {
				t.Fatal(err)
			}
			if want := afinfoDuration(t, path); !near(got, want, 0.01) {
				t.Errorf("Duration = %.3f, afinfo = %.3f", got, want)
			}
		})
	}
}

func TestPrepareAIFF(t *testing.T) {
	for name, src := range aiffFixtures(t) {
		t.Run(name, func(t *testing.T) {
			info, err := parseAIFF(src)
			if err != nil {
				t.Fatal(err)
			}
			total, _ := Duration(src)
			pieces, err := Prepare(Track{Path: src, Format: FormatAIFF}, t.TempDir(), 2)
			if err != nil {
				t.Fatal(err)
			}
			checkPieces(t, pieces, total, 2, "audio/aiff")
			for i, p := range pieces {
				if filepath.Ext(p.Path) != ".aiff" {
					t.Errorf("piece %d path = %s", i, p.Path)
				}
				data, err := os.ReadFile(p.Path)
				if err != nil {
					t.Fatal(err)
				}
				if got := string(data[8:12]); got != info.formType {
					t.Errorf("piece %d form type = %s, want %s", i, got, info.formType)
				}
				pi, err := parseAIFF(p.Path)
				if err != nil {
					t.Fatalf("piece %d: %v", i, err)
				}
				if !near(pi.seconds(), p.Seconds, 1e-9) {
					t.Errorf("piece %d: parsed %v, Seconds %v", i, pi.seconds(), p.Seconds)
				}
				wav := filepath.Join(t.TempDir(), "decoded.wav")
				if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", p.Path, wav).CombinedOutput(); err != nil {
					t.Errorf("piece %d decode: %v\n%s", i, err, out)
				}
			}
		})
	}
}

func TestAIFFWhole(t *testing.T) {
	for name, src := range aiffFixtures(t) {
		t.Run(name, func(t *testing.T) {
			total, _ := Duration(src)
			for _, maxSeconds := range []float64{0, total + 1} {
				pieces, err := Prepare(Track{Path: src, Format: FormatAIFF}, t.TempDir(), maxSeconds)
				if err != nil {
					t.Fatal(err)
				}
				if len(pieces) != 1 || pieces[0].MIME != "audio/aiff" || !near(pieces[0].Seconds, total, 1e-9) {
					t.Fatalf("maxSeconds %v: pieces = %+v", maxSeconds, pieces)
				}
				a, _ := os.ReadFile(src)
				b, _ := os.ReadFile(pieces[0].Path)
				if !bytes.Equal(a, b) {
					t.Error("file that fits should be published as-is")
				}
			}
		})
	}
}

func TestAIFFErrors(t *testing.T) {
	fx := aiffFixtures(t)
	valid, err := os.ReadFile(fx["aiff.aiff"])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cases := map[string][]byte{
		"random.aiff":    []byte("this is not an aiff file"),
		"truncComm.aiff": valid[:25], // COMM body runs past the end
		"noSSND.aiff":    valid[:38], // COMM complete, no sound chunk
	}
	for name, data := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Duration(path); !errors.Is(err, errAIFF) {
			t.Errorf("%s: err = %v, want errAIFF", name, err)
		}
		if _, err := Prepare(Track{Path: path, Format: FormatAIFF}, dir, 2); !errors.Is(err, errAIFF) {
			t.Errorf("%s: Prepare err = %v, want errAIFF", name, err)
		}
	}
}

func TestAIFFCompressedUnsupported(t *testing.T) {
	src := aiffFixtures(t)["aiff.aiff"]
	path := convertAIFF(t, src, "ulaw.aiff", "-f", "AIFC", "-d", "ulaw")
	_, err := Duration(path)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Duration err = %v, want ErrUnsupported", err)
	}
	if _, err := Prepare(Track{Path: path, Format: FormatAIFF}, t.TempDir(), 2); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Prepare err = %v, want ErrUnsupported", err)
	}
}
