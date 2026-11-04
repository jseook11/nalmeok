//go:build windows

package notify

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/jseook11/nalmeok/assets"
)

// createNoWindow keeps powershell.exe, a console program, from opening a
// console window.
const createNoWindow = 0x08000000

func send(title, message string) {
	script := packagedToastScript(packageAppID(), message)
	if packageAppID() == "" {
		script = toastScript(windowsAppID, "Nalmeok", toastIcon(), message)
	}
	cmd := exec.Command(powershellPath(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePowerShell(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		log.Printf("notify: %v", err)
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("notify: powershell: %v", err)
		}
	}()
}

func install() {}

func powershellPath() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return "powershell.exe"
}

// toastIcon writes the icon to %LOCALAPPDATA%\Nalmeok\notify-icon.png once
// per run and returns its path, or "" (Windows then shows a plain icon).
var toastIcon = sync.OnceValue(func() string {
	png := largestPNG(assets.ICO)
	if png == nil {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, "Nalmeok", "notify-icon.png")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, png) {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return ""
	}
	return path
})

// packageAppID returns the AppUserModelID Windows gave this process from its
// MSIX package (<PackageFamilyName>!App), or "" for the portable exe.
var packageAppID = sync.OnceValue(func() string {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentApplicationUserModelId")
	if proc.Find() != nil {
		return ""
	}
	const errorSuccess, errorInsufficientBuffer = 0, 122
	n := uint32(0)
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&n)), 0); r != errorInsufficientBuffer || n == 0 {
		return "" // APPMODEL_ERROR_NO_APPLICATION: not packaged
	}
	buf := make([]uint16, n)
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&buf[0]))); r != errorSuccess {
		return ""
	}
	return syscall.UTF16ToString(buf)
})
