package app

import (
	"errors"
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

// withArchive gives the test app a transcripts folder and returns it.
func withArchive(ta *testApp) string {
	dir := filepath.Join(ta.dir, "transcripts")
	ta.app.TranscriptsDir = dir
	return dir
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadOutputPrefersOriginal(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	path := filepath.Join(ta.dir, "lec.md")
	writeText(t, path, "고친 내용\n")
	id := addDone(t, ta, path)
	writeText(t, filepath.Join(archive, id+".md"), "처음 내용\n")
	got, err := ta.app.ReadOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "고친 내용\n" || got.Moved {
		t.Fatalf("ReadOutput = %+v, want the original file", got)
	}
}

func TestReadOutputFallsBackToArchive(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	id := addDone(t, ta, filepath.Join(ta.dir, "gone.md"))
	writeText(t, filepath.Join(archive, id+".md"), "보관된 내용\n")
	got, err := ta.app.ReadOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "보관된 내용\n" || !got.Moved {
		t.Fatalf("ReadOutput = %+v, want the archived text marked moved", got)
	}
}

func TestReadOutputArchivesOldItem(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	path := filepath.Join(ta.dir, "lec.md")
	writeText(t, path, "예전 항목\n")
	id := addDone(t, ta, path)
	if _, err := ta.app.ReadOutput(id); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(archive, id+".md"))
	if err != nil || string(data) != "예전 항목\n" {
		t.Fatalf("archive = %q, %v", data, err)
	}
	// Once copied, the .md can move and the text is still there.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := ta.app.ReadOutput(id)
	if err != nil || got.Text != "예전 항목\n" || !got.Moved {
		t.Fatalf("ReadOutput = %+v, %v", got, err)
	}
}

func TestArchiveFinishedCopiesDoneItems(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	path := filepath.Join(ta.dir, "lec.md")
	writeText(t, path, "본문\n")
	id := addDone(t, ta, path)
	failed := store.Item{ID: "failed-1", Name: "x", Kind: "file", State: "failed", Output: path}
	if err := ta.app.store.Add(failed); err != nil {
		t.Fatal(err)
	}
	ta.app.archiveFinished()
	if data, err := os.ReadFile(filepath.Join(archive, id+".md")); err != nil || string(data) != "본문\n" {
		t.Fatalf("archive = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(archive, failed.ID+".md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unfinished item archived: %v", err)
	}
}

func TestRemoveDeletesArchive(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	path := filepath.Join(ta.dir, "lec.md")
	writeText(t, path, "본문\n")
	id := addDone(t, ta, path)
	copyPath := filepath.Join(archive, id+".md")
	writeText(t, copyPath, "본문\n")
	if err := ta.app.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive not removed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the .md must stay: %v", err)
	}
}

func TestRestoreOutputWritesNextToSource(t *testing.T) {
	ta := newTestApp(t)
	archive := withArchive(ta)
	src := filepath.Join(ta.dir, "src", "lec.m4a")
	writeText(t, src, "audio")
	// Another file already took the name; the restored copy must not replace it.
	taken := filepath.Join(ta.dir, "src", "lec.md")
	writeText(t, taken, "다른 파일\n")
	item := store.Item{ID: "done-1", Name: "lec.m4a", Kind: "file", Source: src, State: "done",
		Output: filepath.Join(ta.dir, "moved", "lec.md")}
	if err := ta.app.store.Add(item); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(archive, item.ID+".md"), "보관된 내용\n")

	if err := ta.app.RestoreOutput(item.ID); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ta.dir, "src", "lec (2).md")
	got, _ := ta.app.store.Get(item.ID)
	if got.Output != want {
		t.Fatalf("Output = %q, want %q", got.Output, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "보관된 내용\n" {
		t.Fatalf("restored = %q, %v", data, err)
	}
	if data, _ := os.ReadFile(taken); string(data) != "다른 파일\n" {
		t.Fatal("existing file was replaced")
	}
}

func TestRestoreOutputWithoutArchive(t *testing.T) {
	ta := newTestApp(t)
	withArchive(ta)
	id := addDone(t, ta, filepath.Join(ta.dir, "gone.md"))
	if err := ta.app.RestoreOutput(id); err == nil {
		t.Fatal("RestoreOutput without an archived copy must fail")
	}
}

func TestListItemsMarksMissingOutput(t *testing.T) {
	ta := newTestApp(t)
	here := filepath.Join(ta.dir, "here.md")
	writeText(t, here, "본문\n")
	items := []store.Item{
		{ID: "here", Name: "here", Kind: "file", State: "done", Output: here},
		{ID: "gone", Name: "gone", Kind: "file", State: "done", Output: filepath.Join(ta.dir, "gone.md")},
		{ID: "failed", Name: "failed", Kind: "file", State: "failed"},
	}
	for _, it := range items {
		if err := ta.app.store.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range ta.app.ListItems("") {
		if want := it.ID == "gone"; it.OutputMissing != want {
			t.Errorf("%s: OutputMissing = %v, want %v", it.ID, it.OutputMissing, want)
		}
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
