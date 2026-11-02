//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func openPath(path string) error { return startExplorer(path, false) }

func revealPath(path string) error { return startExplorer(path, true) }

// startExplorer runs explorer.exe with a command line built by
// explorerCmdLine. explorer.exe exits with status 1 even when it did what
// was asked, so its exit status is not read: only a failure to start counts.
// The process is reaped in the background.
func startExplorer(path string, reveal bool) error {
	exe := "explorer.exe"
	if root := os.Getenv("SystemRoot"); root != "" {
		exe = filepath.Join(root, "explorer.exe")
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: explorerCmdLine(path, reveal)}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
