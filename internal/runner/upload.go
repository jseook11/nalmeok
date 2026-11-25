package runner

// Uploads: audio and PDFs go to the Files API once and are reused while
// they stay valid.

import (
	"context"
	"maps"
	"path/filepath"
	"strings"

	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
)

// uploadParts builds the request [pdfs..., audio...] for one key, uploading
// files that are not yet uploaded for that key or are close to the 48 h
// expiry. The prompt goes in as the system instruction.
func (r *Runner) uploadParts(ctx context.Context, client gemini.Client, id, keyID string, unit store.Unit, pdfs []string) (gemini.Request, error) {
	var parts []gemini.Part
	for _, pdf := range pdfs {
		up, err := r.cachedUpload(ctx, client, id, keyID, pdf, mimePDF)
		if err != nil {
			return gemini.Request{}, err
		}
		parts = append(parts, gemini.Part{FileURI: up.URI, MIME: up.MIME})
	}
	for i, path := range unit.Files {
		up, err := r.cachedUpload(ctx, client, id, keyID, path, unitMIME(unit, i))
		if err != nil {
			return gemini.Request{}, err
		}
		parts = append(parts, gemini.Part{FileURI: up.URI, MIME: up.MIME})
	}
	return gemini.Request{System: gemini.PromptFor(len(unit.Files)), Parts: parts}, nil
}

// cachedUpload returns the item's saved upload of path for keyID, uploading
// it when there is none or it is close to the 48 h expiry.
func (r *Runner) cachedUpload(ctx context.Context, client gemini.Client, id, keyID, path, mime string) (store.Upload, error) {
	// Piece files are rewritten in place when an item is prepared again, so
	// the file's size and time are part of the key.
	cacheKey := keyID + "\x00" + path + "\x00" + fileStamp(path)
	item, _ := r.opts.Store.Get(id)
	up, ok := item.Uploads[cacheKey]
	if ok && r.opts.Now().Sub(up.UploadedAt) < uploadMaxAge {
		return up, nil
	}
	up, err := r.upload(ctx, client, path, mime)
	if err != nil {
		return up, err
	}
	// Saved right away, so a crash or restart before Generate still keeps
	// this file; expired entries are dropped so the map does not grow.
	now := r.opts.Now()
	if err := r.update(id, func(it *store.Item) {
		maps.DeleteFunc(it.Uploads, func(_ string, old store.Upload) bool {
			return now.Sub(old.UploadedAt) >= uploadMaxAge
		})
		if it.Uploads == nil {
			it.Uploads = map[string]store.Upload{}
		}
		it.Uploads[cacheKey] = up
	}); err != nil {
		return up, err
	}
	return up, nil
}

func (r *Runner) upload(ctx context.Context, client gemini.Client, path, mime string) (store.Upload, error) {
	var up store.Upload
	err := r.retryTransient(ctx, nil, func() error {
		var uerr error
		up, uerr = client.Upload(ctx, path, mime)
		return uerr
	})
	if err != nil {
		return up, err
	}
	if up.MIME == "" {
		up.MIME = mime
	}
	if up.UploadedAt.IsZero() {
		up.UploadedAt = r.opts.Now()
	}
	return up, nil
}

// unitMIME is the MIME recorded when the piece was prepared. Unit entries
// written by older versions lack MIMEs, so fall back to the extension.
func unitMIME(unit store.Unit, i int) string {
	if i < len(unit.MIMEs) && unit.MIMEs[i] != "" {
		return unit.MIMEs[i]
	}
	if i < len(unit.Files) {
		return mimeForPath(unit.Files[i])
	}
	return "application/octet-stream"
}

func mimeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "audio/mp3"
	case ".wav":
		return "audio/wav"
	case ".pdf":
		return mimePDF
	case ".m4a", ".mp4":
		return "audio/m4a"
	}
	return "application/octet-stream"
}
