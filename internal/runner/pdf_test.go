package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jseook11/nalmeok/internal/gemini"
)

// pdfFolder makes a folder with the given PDFs (and no real audio; the fake
// media invents a track).
func (h *harness) pdfFolder(name string, pdfs ...string) string {
	h.t.Helper()
	folder := filepath.Join(h.dir, "src", name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		h.t.Fatal(err)
	}
	for _, p := range pdfs {
		if err := os.WriteFile(filepath.Join(folder, p), []byte("%PDF-"+p), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	return folder
}

// pdfParts returns, per request, the names of the PDF parts in order. The
// fake upload URI is "<key>:<work file name>"; work files are numbered in
// the chosen order, so the test maps them back through the content.
func pdfURIs(parts []gemini.Part) []string {
	var out []string
	for _, p := range parts {
		if p.MIME == mimePDF {
			out = append(out, p.FileURI[strings.Index(p.FileURI, ":")+1:])
		}
	}
	return out
}

// recordParts captures every free request's parts for one key.
func (h *harness) recordParts(apiKey string) func() [][]gemini.Part {
	var mu sync.Mutex
	var got [][]gemini.Part
	h.world.client(apiKey).gen = func(_ int, parts []gemini.Part) (string, string, error) {
		mu.Lock()
		got = append(got, append([]gemini.Part(nil), parts...))
		mu.Unlock()
		return answer(parts), gemini.FinishStop, nil
	}
	return func() [][]gemini.Part {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

// Without a choice (CLI, or a folder with 0–1 PDF) every PDF in the folder
// goes into every request, before the audio.
func TestFolderSendsEveryPDFByDefault(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	parts := h.recordParts("AIza-f1")
	folder := h.pdfFolder("ch07", "a.pdf", "b.pdf", "c.pdf")
	h.r.Start(h.ctx)
	item := h.addOne(folder, "free")
	done := h.wait(item.ID, StateDone)
	if done.PDFChosen || done.PDFs != nil {
		t.Errorf("no choice expected: %+v %v", done.PDFChosen, done.PDFs)
	}
	reqs := parts()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	want := []string{"reference-001.pdf", "reference-002.pdf", "reference-003.pdf"}
	for _, req := range reqs {
		if got := pdfURIs(req); !slices.Equal(got, want) {
			t.Errorf("PDF parts = %v, want %v", got, want)
		}
		if req[0].MIME != mimePDF || req[len(req)-1].MIME == mimePDF {
			t.Errorf("order must be PDFs, audio: %+v", req)
		}
	}
	// Uploaded once per key, not once per request.
	if n := h.world.client("AIza-f1").uploadCount(); n != 3+2 {
		t.Errorf("uploads = %d, want 5", n)
	}
}

// A chosen subset is stored on the item and is all that is sent; the copies
// in the work directory carry the chosen files' contents in order.
func TestAddWithPDFsSendsOnlyTheChoice(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	folder := h.pdfFolder("ch07", "a.pdf", "b.pdf", "c.pdf")
	chosen := []string{filepath.Join(folder, "c.pdf"), filepath.Join(folder, "a.pdf")}

	var mu sync.Mutex
	var contents [][]string
	h.world.client("AIza-f1").gen = func(_ int, parts []gemini.Part) (string, string, error) {
		var got []string
		for _, uri := range pdfURIs(parts) {
			items := h.st.List("")
			data, _ := os.ReadFile(filepath.Join(h.dir, "cfg", "nalmeok", "work", items[0].ID, uri))
			got = append(got, string(data))
		}
		mu.Lock()
		contents = append(contents, got)
		mu.Unlock()
		return answer(parts), gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	if err := h.r.AddWithPDFs(folder, chosen, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	if !item.PDFChosen || !slices.Equal(item.PDFs, chosen) {
		t.Fatalf("stored choice = %v %v", item.PDFChosen, item.PDFs)
	}
	h.wait(item.ID, StateDone)
	mu.Lock()
	defer mu.Unlock()
	for _, got := range contents {
		if !slices.Equal(got, []string{"%PDF-c.pdf", "%PDF-a.pdf"}) {
			t.Errorf("sent PDFs = %v", got)
		}
	}
}

// "PDF 없이 전사": an empty choice sends no PDF, and moving the item to Batch
// keeps that choice instead of falling back to every PDF.
func TestAddWithNoPDFsSurvivesSendToBatch(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	folder := h.pdfFolder("ch07", "a.pdf", "b.pdf")
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", "", err429()
	}
	h.r.Start(h.ctx)
	if err := h.r.AddWithPDFs(folder, nil, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	if !item.PDFChosen || len(item.PDFs) != 0 {
		t.Fatalf("stored choice = %v %v", item.PDFChosen, item.PDFs)
	}
	h.wait(item.ID, StateFreeExhausted)
	// The work directory is rebuilt from scratch, as after a restart that
	// lost it: the choice must come from the item, not the folder.
	_ = os.RemoveAll(filepath.Join(h.dir, "cfg", "nalmeok", "work", item.ID))
	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	reqs := h.world.requests(waiting.Batch.Name)
	if len(reqs) == 0 {
		t.Fatal("no batch requests")
	}
	for _, req := range reqs {
		if got := pdfURIs(req.Parts); len(got) != 0 {
			t.Errorf("batch request has PDFs %v", got)
		}
	}
}

// A chosen PDF that disappeared fails the item with its name instead of
// silently sending fewer references.
func TestChosenPDFMissingFails(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	folder := h.pdfFolder("ch07", "a.pdf", "b.pdf")
	h.r.Start(h.ctx)
	if err := h.r.AddWithPDFs(folder, []string{filepath.Join(folder, "gone.pdf")}, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "gone.pdf") {
		t.Errorf("Detail = %q", failed.Detail)
	}
}
