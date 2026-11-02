package frontend

import (
	"io/fs"
	"testing"
)

func TestAssetsContainDistFiles(t *testing.T) {
	dist, err := fs.Sub(Assets, "dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	for _, name := range []string{"index.html", "app.css", "app.js", "i18n/ko.json"} {
		if _, err := fs.Stat(dist, name); err != nil {
			t.Fatalf("embedded dist is missing %s: %v", name, err)
		}
	}
}
