//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

// attachParentProcess is ATTACH_PARENT_PROCESS, (DWORD)-1.
const attachParentProcess = ^uint32(0)

// attachParentConsole lets CLI output reach the cmd or PowerShell window the
// exe was started from. The exe is linked with -H windowsgui so the app
// opens without a console; such a process has no stdout or stderr unless
// they were redirected. Redirected handles (a file or pipe) are kept as
// they are; missing ones are bound to the parent's console. Without a
// parent console (double-click, Explorer) nothing changes.
func attachParentConsole() {
	needOut := !usableStdHandle(syscall.STD_OUTPUT_HANDLE)
	needErr := !usableStdHandle(syscall.STD_ERROR_HANDLE)
	if !needOut && !needErr {
		return
	}
	attach := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if ok, _, _ := attach.Call(uintptr(attachParentProcess)); ok == 0 {
		return
	}
	con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	if needOut {
		os.Stdout = con
	}
	if needErr {
		os.Stderr = con
		// The standard logger captured the old os.Stderr at start-up.
		log.SetOutput(con)
	}
}

func usableStdHandle(which int) bool {
	h, err := syscall.GetStdHandle(which)
	if err != nil || h == syscall.InvalidHandle || h == 0 {
		return false
	}
	t, err := syscall.GetFileType(h)
	return err == nil && t != syscall.FILE_TYPE_UNKNOWN
}
