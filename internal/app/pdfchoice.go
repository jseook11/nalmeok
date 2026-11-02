package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/jseook11/nalmeok/internal/media"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// PDFFile is one PDF offered on the PDF sheet. Pages is 0 when unknown.
type PDFFile struct {
	Path  string `json:"Path"`
	Name  string `json:"Name"`
	Pages int    `json:"Pages"`
}

// PDFChoice is a dropped folder with several PDFs, waiting for the user to
// pick which ones the transcription may consult. The window shows one sheet
// per choice, in order, and answers with ConfirmPDFs or CancelPDFs.
type PDFChoice struct {
	Token  string    `json:"Token"`
	Folder string    `json:"Folder"`
	Name   string    `json:"Name"`
	PDFs   []PDFFile `json:"PDFs"`
}

type pendingPDF struct {
	choice PDFChoice
	mode   string
	keyID  string
}

// pdfChoices is the queue of folders waiting on the PDF sheet. It lives in
// memory: quitting the app forgets folders the user never answered for.
type pdfChoices struct {
	mu      sync.Mutex
	next    int
	pending []pendingPDF
}

// holdPDFChoices takes the folders that hold audio and two or more PDFs out
// of paths, queues a PDFChoice for each and tells the window
// ("items:choosePDF"). It returns the paths that can start right away. With
// no key at all nothing is held, so the add fails with the usual note.
func (a *App) holdPDFChoices(paths []string, mode, keyID string) []string {
	if !a.hasKeys() {
		return paths
	}
	rest := paths[:0:0]
	var held []PDFChoice
	for _, path := range paths {
		pdfs := folderNeedsChoice(path)
		if pdfs == nil {
			rest = append(rest, path)
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		choice := PDFChoice{Folder: abs, Name: filepath.Base(abs)}
		for _, p := range pdfs {
			choice.PDFs = append(choice.PDFs, PDFFile{Path: p, Name: filepath.Base(p), Pages: media.PDFPages(p)})
		}
		a.pdf.mu.Lock()
		a.pdf.next++
		choice.Token = "pdf-" + strconv.Itoa(a.pdf.next)
		a.pdf.pending = append(a.pdf.pending, pendingPDF{choice: choice, mode: mode, keyID: keyID})
		a.pdf.mu.Unlock()
		held = append(held, choice)
	}
	if ctx := a.context(); ctx != nil {
		for _, choice := range held {
			wailsruntime.EventsEmit(ctx, "items:choosePDF", choice)
		}
	}
	return rest
}

// folderNeedsChoice returns the PDFs of a folder that holds audio and two or
// more PDFs, or nil. Zero or one PDF starts without asking, as before.
func folderNeedsChoice(path string) []string {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}
	pdfs := media.FolderPDFs(path)
	if len(pdfs) < 2 || !hasAudio(path) {
		return nil
	}
	return pdfs
}

// hasAudio reports whether a folder directly holds a file the app can
// transcribe. A folder without one fails anyway, so asking about its PDFs
// first would only be in the way.
func hasAudio(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") && !e.IsDir() && media.FormatOf(e.Name()) != "" {
			return true
		}
	}
	return false
}

// PendingPDFChoices lists the folders still waiting on the PDF sheet, oldest
// first. The window reads it at start in case an event came before it
// listened.
func (a *App) PendingPDFChoices() []PDFChoice {
	a.pdf.mu.Lock()
	defer a.pdf.mu.Unlock()
	out := make([]PDFChoice, 0, len(a.pdf.pending))
	for _, p := range a.pdf.pending {
		out = append(out, p.choice)
	}
	return out
}

// take removes and returns a pending choice.
func (c *pdfChoices) take(token string) (pendingPDF, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.pending {
		if p.choice.Token == token {
			c.pending = append(c.pending[:i:i], c.pending[i+1:]...)
			return p, true
		}
	}
	return pendingPDF{}, false
}

// ConfirmPDFs adds the waiting folder with the selected PDFs. selected must
// come from the folder's list; unknown paths are ignored and the folder's
// order is kept. An empty selection transcribes without a PDF. An unknown
// token (already answered) does nothing.
func (a *App) ConfirmPDFs(token string, selected []string) error {
	if a.runner == nil {
		return errNotReady
	}
	p, ok := a.pdf.take(token)
	if !ok {
		return nil
	}
	want := make(map[string]bool, len(selected))
	for _, s := range selected {
		want[s] = true
	}
	chosen := []string{}
	for _, f := range p.choice.PDFs {
		if want[f.Path] {
			chosen = append(chosen, f.Path)
		}
	}
	return a.runner.AddWithPDFs(p.choice.Folder, chosen, p.mode, p.keyID)
}

// CancelPDFs drops a waiting folder without adding it.
func (a *App) CancelPDFs(token string) {
	a.pdf.take(token)
}
