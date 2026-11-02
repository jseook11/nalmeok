//go:build !darwin || !cgo

package macdrop

// Install does nothing outside macOS: Windows' WebView2 hands Wails real
// file paths, and there is no file promise to receive.
func Install(dir string, handler func(paths []string)) {}
