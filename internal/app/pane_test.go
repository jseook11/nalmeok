package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/store"
)

func addDone(t *testing.T, ta *testApp, output string) string {
	t.Helper()
	item := store.Item{ID: "done-1", Name: "lec", Kind: "file", State: "done", Output: output}
	if err := ta.app.store.Add(item); err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func TestReadOutputReturnsText(t *testing.T) {
	ta := newTestApp(t)
	path := filepath.Join(ta.dir, "lec.md")
	if err := os.WriteFile(path, []byte("첫 문단\n\n둘째 문단\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := addDone(t, ta, path)
	got, err := ta.app.ReadOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "첫 문단\n\n둘째 문단\n" || got.Truncated {
		t.Fatalf("ReadOutput = %+v", got)
	}
}

func TestReadOutputMovedFile(t *testing.T) {
	ta := newTestApp(t)
	id := addDone(t, ta, filepath.Join(ta.dir, "gone.md"))
	if _, err := ta.app.ReadOutput(id); err == nil || !strings.Contains(err.Error(), "옮겼") {
		t.Fatalf("err = %v, want the moved-file message", err)
	}
}

func TestReadOutputIsBounded(t *testing.T) {
	ta := newTestApp(t)
	path := filepath.Join(ta.dir, "big.md")
	// Multi-byte text so the cut must land on a rune boundary.
	if err := os.WriteFile(path, []byte(strings.Repeat("가", MaxOutputRead/3+10)), 0o600); err != nil {
		t.Fatal(err)
	}
	id := addDone(t, ta, path)
	got, err := ta.app.ReadOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Text) > MaxOutputRead || !strings.HasSuffix(got.Text, "가") {
		t.Fatalf("Truncated = %v, len = %d", got.Truncated, len(got.Text))
	}
}

// A stray invalid byte inside the kept part (a transcript re-saved in
// another encoding) must not make the cut walk back to it: only a rune split
// by the limit is dropped.
func TestReadOutputCutKeepsTextBeforeLimit(t *testing.T) {
	ta := newTestApp(t)
	path := filepath.Join(ta.dir, "odd.md")
	data := []byte(strings.Repeat("a", MaxOutputRead-4000))
	data = append(data, 0xff)
	data = append(data, strings.Repeat("b", 8000)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	id := addDone(t, ta, path)
	got, err := ta.app.ReadOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Text) != MaxOutputRead {
		t.Fatalf("Truncated = %v, len = %d, want %d", got.Truncated, len(got.Text), MaxOutputRead)
	}
}

func TestPaneWidths(t *testing.T) {
	tests := []struct {
		name             string
		open             bool
		width, screen    int
		listWidth        int
		remember, target int
	}{
		{"open from the list width", true, ListWidth, 0, 0, ListWidth, PaneWidth},
		{"close back to the list width", false, PaneWidth, 0, ListWidth, ListWidth, ListWidth},
		{"open a window already wide", true, 1200, 0, 0, 1200, 0},
		{"close keeps the wide window", false, 1200, 0, 1200, 1200, 0},
		{"open on a narrower screen", true, ListWidth, 900, 0, ListWidth, 900},
		{"close with no remembered width", false, PaneWidth, 0, 0, ListWidth, ListWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remember, target := paneWidths(tt.open, tt.width, tt.screen, tt.listWidth)
			if remember != tt.remember || target != tt.target {
				t.Fatalf("paneWidths(%v, %d, %d, %d) = (%d, %d), want (%d, %d)",
					tt.open, tt.width, tt.screen, tt.listWidth, remember, target, tt.remember, tt.target)
			}
		})
	}
}

func TestSetPaneRemembersChoice(t *testing.T) {
	ta := newTestApp(t)
	if ta.app.PaneOpen() {
		t.Fatal("pane must start hidden")
	}
	if err := ta.app.SetPane(true); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(ta.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Pane() || !ta.app.PaneOpen() {
		t.Fatal("pane choice not saved")
	}
}
