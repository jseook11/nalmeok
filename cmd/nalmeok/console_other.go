//go:build !windows

package main

// attachParentConsole is only needed on Windows (see console_windows.go).
func attachParentConsole() {}
