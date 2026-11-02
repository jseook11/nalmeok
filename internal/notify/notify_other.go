//go:build !darwin

package notify

func send(title, message string) {}

func install() {}
