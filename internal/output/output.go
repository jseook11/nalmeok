// Package output merges transcription chunks and writes .md files next to
// their source without ever overwriting an existing result (see WriteNew).
package output

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// Merge trims every chunk, drops empty chunks, and joins the rest with one
// blank line. The result always ends with a single newline.
func Merge(texts []string) string {
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// TargetPath computes the .md location for a source.
//
// A file keeps its directory and drops its extension; a folder gets
// "<folder name>.md" inside itself.
func TargetPath(source, kind string) string {
	source = filepath.Clean(source)
	if kind == "folder" {
		name := filepath.Base(source)
		return filepath.Join(source, name+".md")
	}
	dir := filepath.Dir(source)
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+".md")
}

// UniquePath returns path when it does not exist, otherwise
// "name (2).md", "name (3).md", and so on.
func UniquePath(path string) string {
	if !exists(path) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !exists(candidate) {
			return candidate
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Write stores text at path atomically (same-directory temp file + rename).
func Write(path, text string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".nalmeok-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.Rename(tmpName, path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	_ = os.Remove(tmpName)
	return lastErr
}

// WriteNew writes text to path, or to "name (2).md", "name (3).md"... when
// that name is taken, and returns the path it used. The name is reserved with
// an exclusive create before the text goes in, so two writers never pick the
// same file and an existing file is never replaced.
func WriteNew(path, text string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; i <= 9999; i++ {
		candidate := path
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(candidate)
			return "", err
		}
		// The reserved empty file is replaced atomically by the full text.
		if err := Write(candidate, text); err != nil {
			_ = os.Remove(candidate)
			return "", err
		}
		return candidate, nil
	}
	return "", i18n.Error("output.nameFailed", "name", filepath.Base(path))
}
