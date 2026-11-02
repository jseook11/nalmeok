//go:build !windows

package config

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// lockFile holds an exclusive flock on path. The kernel drops it when the
// process exits, so a crash never leaves the folder locked.
func lockFile(path string) (io.Closer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}
