#ifndef NALMEOK_NOTIFY_H
#define NALMEOK_NOTIFY_H

// 1 when the process runs inside an .app bundle with a bundle identifier,
// which UNUserNotificationCenter requires (it throws otherwise).
int notifyAvailable(void);

// Sets the notification centre delegate so clicks reach notifyClicked.
// Only call when notifyAvailable() is 1. Safe from any thread.
void notifyInstall(void);

// Asks for permission the first time, then posts one notification, or
// nothing if the user refused. Only call when notifyAvailable() is 1.
// Returns at once.
void notifySend(const char *title, const char *body);

#endif
