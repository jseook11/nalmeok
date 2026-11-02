//go:build !darwin && !windows

package notify

func send(title, message string) {}

func install() {}
