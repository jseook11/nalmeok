package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
)

func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFolderWithSeveralPDFsWaitsForChoice(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.AddKey("", "free", "AIzaFAKE-free-0001"); err != nil {
		t.Fatal(err)
	}
	many := filepath.Join(ta.dir, "ch07")
	writeFiles(t, many, "1강.m4a", "07_hazard.pdf", "07_pipelining.pdf", "과제7.pdf")
	one := filepath.Join(ta.dir, "ch08")
	writeFiles(t, one, "1강.m4a", "용어.pdf")
	noAudio := filepath.Join(ta.dir, "slides")
	writeFiles(t, noAudio, "a.pdf", "b.pdf")
	other := filepath.Join(ta.dir, "ch09")
	writeFiles(t, other, "a.mp3", "x.pdf", "y.pdf")

	if err := ta.app.AddPaths([]string{many, one, noAudio, other}); err != nil {
		t.Fatal(err)
	}
	// ch08 (one PDF) and slides (no audio, fails as before) start now.
	if n := len(ta.app.ListItems("")); n != 2 {
		t.Fatalf("items added now = %d, want 2", n)
	}
	pending := ta.app.PendingPDFChoices()
	if len(pending) != 2 || pending[0].Folder != many || pending[1].Folder != other {
		t.Fatalf("pending = %+v", pending)
	}
	var names []string
	for _, f := range pending[0].PDFs {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"07_hazard.pdf", "07_pipelining.pdf", "과제7.pdf"}) || pending[0].Name != "ch07" {
		t.Fatalf("choice = %+v", pending[0])
	}

	// Picks outside the list are ignored; the folder's order is kept.
	pick := []string{filepath.Join(many, "과제7.pdf"), "/etc/passwd", filepath.Join(many, "07_hazard.pdf")}
	if err := ta.app.ConfirmPDFs(pending[0].Token, pick); err != nil {
		t.Fatal(err)
	}
	items := ta.app.ListItems("")
	if len(items) != 3 || items[0].Source != many {
		t.Fatalf("items = %d", len(items))
	}
	want := []string{filepath.Join(many, "07_hazard.pdf"), filepath.Join(many, "과제7.pdf")}
	if !items[0].PDFChosen || !slices.Equal(items[0].PDFs, want) {
		t.Errorf("stored choice = %v %v", items[0].PDFChosen, items[0].PDFs)
	}
	// Answering twice adds nothing.
	if err := ta.app.ConfirmPDFs(pending[0].Token, nil); err != nil || len(ta.app.ListItems("")) != 3 {
		t.Errorf("second confirm: %v", err)
	}

	ta.app.CancelPDFs(pending[1].Token)
	if len(ta.app.PendingPDFChoices()) != 0 || len(ta.app.ListItems("")) != 3 {
		t.Error("cancel should drop the folder")
	}
}

func TestConfirmWithoutPDFsStoresEmptyChoice(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.AddKey("", "free", "AIzaFAKE-free-0001"); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(ta.dir, "ch07")
	writeFiles(t, folder, "1강.wav", "a.pdf", "b.pdf")
	if err := ta.app.AddPaths([]string{folder}); err != nil {
		t.Fatal(err)
	}
	pending := ta.app.PendingPDFChoices()
	if len(pending) != 1 {
		t.Fatalf("pending = %d", len(pending))
	}
	if err := ta.app.ConfirmPDFs(pending[0].Token, []string{}); err != nil {
		t.Fatal(err)
	}
	items := ta.app.ListItems("")
	if len(items) != 1 || !items[0].PDFChosen || len(items[0].PDFs) != 0 {
		t.Fatalf("items = %+v", items)
	}
}

// Without any key nothing waits on the sheet: the add fails with the usual
// key note instead.
func TestNoKeysDoesNotHoldPDFFolders(t *testing.T) {
	ta := newTestApp(t)
	folder := filepath.Join(ta.dir, "ch07")
	writeFiles(t, folder, "1강.m4a", "a.pdf", "b.pdf")
	_ = ta.app.AddPaths([]string{folder})
	if len(ta.app.PendingPDFChoices()) != 0 {
		t.Error("held a folder with no keys")
	}
}

func TestCodexHoldsPDFFoldersOnlyForPaid(t *testing.T) {
	ta := newTestApp(t)
	ta.app.CodexAuth = validCodexAuth
	ta.app.Experiment = true
	if _, err := ta.app.cfg.AddKey("Paid 1", "paid", "AIza-paid"); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.cfg.SetBackend(config.BackendCodex); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(ta.dir, "ch07")
	writeFiles(t, folder, "1강.m4a", "a.pdf", "b.pdf")

	// Codex (free): no PDFs to choose, the folder is added directly.
	if got := ta.app.holdPDFChoices([]string{folder}, "free", ""); len(got) != 1 {
		t.Fatalf("free: rest = %v, want the folder", got)
	}
	// Paid goes to Gemini Batch, which sends PDFs: ask first.
	if got := ta.app.holdPDFChoices([]string{folder}, "batch", ""); len(got) != 0 {
		t.Fatalf("batch: rest = %v, want held", got)
	}
	if n := len(ta.app.PendingPDFChoices()); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
}
