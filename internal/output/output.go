// Package output merges transcription chunks and writes .md files into the
// transcript folder without ever overwriting an existing result (see
// WriteNew), and names the app's own copy of each result (see ArchivePath).
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

// TargetPath is where a new transcript of source goes: "<root>/<name>.md".
// A file's name drops its extension; a folder's name is kept as is. Every
// transcript lands in root rather than next to its recording, so the app
// never writes into the user's folders (on macOS, Desktop, Documents and
// Downloads each ask permission).
func TargetPath(root, source, kind string) string {
	return filepath.Join(root, Name(source, kind)+".md")
}

// Name is the transcript name a source gets: a file's name without its
// extension, or a folder's name as it is ("algorithm_09.1" keeps its ".1").
func Name(source, kind string) string {
	base := filepath.Base(filepath.Clean(source))
	if kind == "folder" {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Inside reports whether path is in dir or a folder below it. Both are
// compared cleaned; an empty dir holds nothing.
func Inside(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ArchivePath is where the app keeps its copy of an item's transcript,
// "<dir>/<id>.md". The item ID names it, so renaming or moving the .md next
// to the transcript never affects it. An empty dir means no archive ("").
func ArchivePath(dir, id string) string {
	if dir == "" || id == "" {
		return ""
	}
	return filepath.Join(dir, id+".md")
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
// that name is taken, and returns the path it used. The name is reserved
// first (Reserve), so two writers never pick the same file and an existing
// file is never replaced.
func WriteNew(path, text string) (string, error) {
	candidate, err := Reserve(path)
	if err != nil {
		return "", err
	}
	// The reserved empty file is replaced atomically by the full text.
	if err := Write(candidate, text); err != nil {
		_ = os.Remove(candidate)
		return "", err
	}
	return candidate, nil
}

// Reserve claims path, or "name (2).ext", "name (3).ext"... when that name is
// taken, by creating it empty with an exclusive create, and returns the name
// it claimed. The caller then replaces the empty file (a write or a rename).
// Unlike UniquePath, two callers at once never get the same name.
func Reserve(path string) (string, error) {
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
		return candidate, nil
	}
	return "", i18n.Error("output.nameFailed", "name", filepath.Base(path))
}
