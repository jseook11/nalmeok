//go:build windows

package config

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION.
const errSharingViolation syscall.Errno = 32

// lockFile opens path with no sharing, so a second open fails until this
// handle is closed. Windows closes it when the process exits.
func lockFile(path string) (io.Closer, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
