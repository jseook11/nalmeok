//go:build darwin && cgo

package main

// Wails' macOS dialog code references UTType. The Wails CLI normally injects
// this linker flag; without the CLI we carry it here so `go build` links.

/*
#cgo LDFLAGS: -framework UniformTypeIdentifiers
*/
import "C"
