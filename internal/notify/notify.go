// Package notify shows the "transcription finished" notification.
//
//   - macOS, inside Nalmeok.app: UNUserNotificationCenter, so the
//     notification carries the app's name and icon and a click brings the
//     app forward (see OnClick). If the user refuses notifications nothing
//     is shown: there is no fallback inside the app.
//   - macOS, outside a bundle (build/nalmeok, CLI): osascript, as before.
//     UNUserNotificationCenter throws without a bundle identifier.
//   - Windows: a toast through PowerShell's WinRT bridge, under an app ID
//     registered for the current user (no installer, no extra packages).
//   - elsewhere: nothing.
//
// Send never waits for the notification to be shown.
package notify

import "sync"

var (
	mu      sync.Mutex
	onClick func()
)

// Send shows one notification. title is the bold line; Windows shows only
// message because the toast header already names the app.
func Send(title, message string) {
	send(title, message)
}

// OnClick sets what runs (on its own goroutine) when the user clicks a
// notification. Only the macOS app bundle reports clicks; the app is already
// activated by then. Call it once the window exists.
func OnClick(fn func()) {
	mu.Lock()
	onClick = fn
	mu.Unlock()
	install()
}

func clicked() {
	mu.Lock()
	fn := onClick
	mu.Unlock()
	if fn != nil {
		go fn()
	}
}
