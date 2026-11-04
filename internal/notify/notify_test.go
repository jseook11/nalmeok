package notify

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jseook11/nalmeok/assets"
)

func TestPSQuote(t *testing.T) {
	cases := map[string]string{
		`강의 1주차`:     `'강의 1주차'`,
		`it's`:       `'it''s'`,
		"a\u2019b":   "'a\u2019\u2019b'",
		`$env:X "q"`: `'$env:X "q"'`,
		"":           `''`,
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToastScript(t *testing.T) {
	s := toastScript(windowsAppID, "Nalmeok", `C:\Users\me\AppData\Local\Nalmeok\notify-icon.png`, "강의's 녹음 전사 완료")
	for _, want := range []string{
		"$id = 'kr.nalmeok.app'",
		`HKCU:\Software\Classes\AppUserModelId\`,
		"-Name DisplayName -Value 'Nalmeok'",
		`-Name IconUri -Value 'C:\Users\me\AppData\Local\Nalmeok\notify-icon.png'`,
		"CreateTextNode('강의''s 녹음 전사 완료')",
		"ToastText01",
		"CreateToastNotifier($id)",
		"catch { $id = '" + powershellAppID + "' }",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(toastScript(windowsAppID, "Nalmeok", "", "x"), "IconUri") {
		t.Error("IconUri set without an icon")
	}
}

func TestPackagedToastScript(t *testing.T) {
	s := packagedToastScript("seook.Nalmeok_abc123!App", "done")
	for _, want := range []string{"$id = 'seook.Nalmeok_abc123!App'", "CreateTextNode('done')", "CreateToastNotifier($id)"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "HKCU") || strings.Contains(s, "catch") {
		t.Errorf("packaged script registers an app ID:\n%s", s)
	}
}

func TestEncodePowerShell(t *testing.T) {
	in := "Write-Output '전사 완료'"
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell(in))
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	if got := string(utf16.Decode(units)); got != in {
		t.Fatalf("round trip = %q, want %q", got, in)
	}
}

func TestLargestPNG(t *testing.T) {
	png := largestPNG(assets.ICO)
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatal("no PNG in assets/nalmeok.ico")
	}
	// The 256px entry is the largest one in the committed icon.
	if w := binary.BigEndian.Uint32(png[16:]); w != 256 {
		t.Fatalf("width %d, want 256", w)
	}
	for _, bad := range [][]byte{nil, []byte("not an icon"), {0, 0, 1, 0, 1, 0}} {
		if largestPNG(bad) != nil {
			t.Errorf("largestPNG(%q) should be nil", bad)
		}
	}
	// An entry pointing past the end is skipped, not read.
	broken := []byte{0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 22, 0, 0, 0}
	if largestPNG(broken) != nil {
		t.Error("out-of-range entry read")
	}
}
