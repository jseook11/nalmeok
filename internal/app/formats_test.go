package app

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/frontend"
	"github.com/jseook11/nalmeok/internal/media"
)

// TestAudioExtensionsMatchMedia keeps app.js's display-name regex in step
// with the formats the Go side accepts.
func TestAudioExtensionsMatchMedia(t *testing.T) {
	js, err := fs.ReadFile(frontend.Assets, "dist/app.js")
	if err != nil {
		t.Fatal(err)
	}
	want := "const AUDIO_EXT = /\\.(" + strings.Join(media.Extensions(), "|") + ")$/i;"
	if !strings.Contains(string(js), want) {
		t.Errorf("app.js AUDIO_EXT does not list media.Extensions(); want %s", want)
	}
}
