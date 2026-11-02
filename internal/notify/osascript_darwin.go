//go:build darwin

package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// osascriptSend is the notification for runs outside the app bundle. macOS
// shows it under Script Editor's name and icon.
func osascriptSend(title, message string) {
	_ = exec.Command("osascript", "-e", osascriptLine(title, message)).Run()
}

func osascriptLine(title, message string) string {
	return fmt.Sprintf("display notification %s with title %s", appleQuote(message), appleQuote(title))
}

func appleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
