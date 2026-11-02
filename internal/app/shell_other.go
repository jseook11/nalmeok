//go:build !windows

package app

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

func openPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Run()
	}
	return exec.Command("xdg-open", path).Run()
}

func revealPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Run()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Run()
}
