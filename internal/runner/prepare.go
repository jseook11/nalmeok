package runner

// Preparation: scanning a source, converting and cutting it into units.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/plan"
	"github.com/jseook11/nalmeok/internal/store"
)

func (r *Runner) workDir(id string) string {
	return filepath.Join(r.opts.WorkDir, id)
}

// prepare makes sure every unfinished unit has its piece files on disk,
// re-scanning the source when the work directory is gone.
func (r *Runner) prepare(ctx context.Context, id string) (store.Item, error) {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return item, store.ErrNotFound
	}
	sig := sourceSig(item.Source)
	if item.SourceSig != "" && sig != "" && sig != item.SourceSig {
		// The audio changed since its pieces were made: finished texts and
		// uploads belong to the old audio.
		r.logf("item %s source changed, starting over", id)
		if err := r.update(id, func(it *store.Item) {
			it.Units = nil
			it.Batch = nil
			it.Uploads = nil
			it.Progress = 0
		}); err != nil {
			return item, err
		}
		_ = os.RemoveAll(r.workDir(id))
		item.Units = nil
		item.Batch = nil
		item.Uploads = nil
	}
	if len(item.Units) > 0 && unitsReady(item.Units) {
		if item.SourceSig == "" && sig != "" {
			_ = r.update(id, func(it *store.Item) { it.SourceSig = sig })
		}
		return item, nil
	}
	if err := ctx.Err(); err != nil {
		return item, err
	}
	r.setDetail(id, i18n.T("status.preparingAudio"))
	// Fresh pieces are written below, so uploads of the old ones are useless.
	if err := r.update(id, func(it *store.Item) {
		if it.Batch != nil && it.Batch.Name == "" {
			it.Batch.Uploads = nil
		}
		it.Uploads = nil
	}); err != nil {
		return item, err
	}

	input, err := r.opts.Media.Scan(item.Source)
	if err != nil {
		return item, userError{err}
	}
	dir := r.workDir(id)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return item, err
	}
	tracks := make([][]Piece, 0, len(input.Tracks))
	for _, track := range input.Tracks {
		if err := ctx.Err(); err != nil {
			return item, err
		}
		ps, err := r.opts.Media.Prepare(track, dir, plan.EvenSeconds(track.Seconds, r.unitMax(item.Mode)))
		if err != nil {
			return item, userError{err}
		}
		tracks = append(tracks, ps)
	}
	pieces := 0
	for _, ps := range tracks {
		pieces += len(ps)
	}
	if pieces == 0 {
		return item, userError{i18n.Error("error.noAudio")}
	}
	pdfs := input.PDFs
	if item.PDFChosen {
		pdfs = item.PDFs
	}
	for i, p := range pdfs {
		if err := copyFile(p, filepath.Join(dir, fmt.Sprintf(pdfPattern, i+1))); err != nil {
			return item, userError{i18n.Error("media.openFailed", "name", filepath.Base(p))}
		}
	}
	plans := r.opts.Media.Units(tracks, r.unitMax(item.Mode))
	units := make([]store.Unit, 0, len(plans))
	var total float64
	for i, p := range plans {
		files := make([]string, 0, len(p.Pieces))
		mimes := make([]string, 0, len(p.Pieces))
		for _, piece := range p.Pieces {
			files = append(files, piece.Path)
			mimes = append(mimes, piece.MIME)
		}
		units = append(units, store.Unit{Index: i, Files: files, MIMEs: mimes, Seconds: p.Seconds})
		total += p.Seconds
	}
	// The plan is deterministic, so finished texts survive a re-prepare when
	// the unit layout is unchanged (the source itself is checked above).
	if len(item.Units) == len(units) {
		for i := range units {
			if math.Abs(item.Units[i].Seconds-units[i].Seconds) < 1 {
				units[i].Text = item.Units[i].Text
				units[i].Usage = item.Units[i].Usage
				units[i].Model = item.Units[i].Model
			}
		}
	}
	err = r.update(id, func(it *store.Item) {
		// A name the user changed while the item waited stays.
		if input.Name != "" && it.Name == filepath.Base(it.Source) {
			it.Name = input.Name
		}
		if input.Kind != "" {
			it.Kind = input.Kind
		}
		it.Units = units
		it.AudioSec = total
		it.EstimateKRW = plan.EstimateMidKRW(total)
		it.Progress = doneRatio(units)
		it.SourceSig = sig
	})
	if err != nil {
		return item, err
	}
	item, _ = r.opts.Store.Get(id)
	return item, nil
}

// audioExts are the files whose change makes earlier results stale.
var audioExts = map[string]bool{".m4a": true, ".mp4": true, ".mp3": true, ".wav": true, ".pdf": true}

// sourceSig fingerprints a dropped file, or the audio and PDF files directly
// inside a dropped folder, by name, size and modification time. It is "" when
// the source cannot be read.
func sourceSig(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		return fileSig(info)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !audioExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		b.WriteString(name + ":" + fileSig(info) + ";")
	}
	return b.String()
}

func fileSig(info os.FileInfo) string {
	return fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano())
}

// fileStamp is fileSig for a path, or "" when it cannot be read.
func fileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fileSig(info)
}

func unitsReady(units []store.Unit) bool {
	for _, u := range units {
		if u.Text != "" {
			continue
		}
		if len(u.Files) == 0 {
			return false
		}
		for _, f := range u.Files {
			if _, err := os.Stat(f); err != nil {
				return false
			}
		}
	}
	return true
}

// pdfPaths lists the reference PDFs prepared for an item, in the order they
// go into each request.
func (r *Runner) pdfPaths(id string) []string {
	dir := r.workDir(id)
	entries, err := os.ReadDir(dir) // sorted by name
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		// Match the name only: the work directory itself may hold glob
		// characters.
		if ok, _ := filepath.Match(pdfGlob, e.Name()); ok && e.Type().IsRegular() {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths
}
