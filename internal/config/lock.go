package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ErrLocked means another Nalmeok process (app or CLI) is using the config
// folder.
var ErrLocked = errors.New("Nalmeok이 이미 실행 중이에요")

// lockName is the lock file inside the config folder.
const lockName = "nalmeok.lock"

// Lock takes the config folder for this process. Only one process at a time
// may run the app or the CLI on a folder: two runners would resume the same
// items and transcribe or submit them twice. The lock ends when the returned
// closer is closed or the process exits.
func Lock() (io.Closer, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LockDir(dir)
}

// LockDir is Lock for an explicit folder.
func LockDir(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(dir, lockName))
}
