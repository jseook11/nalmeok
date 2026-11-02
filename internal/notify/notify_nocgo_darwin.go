//go:build darwin && !cgo

package notify

func send(title, message string) { osascriptSend(title, message) }

func install() {}
