#ifndef NALMEOK_MACDROP_H
#define NALMEOK_MACDROP_H

// Hooks Wails' WailsWebView so file-promise drops (Voice Memos) are received.
// Safe to call from any thread; the work happens on the main queue.
void macdropInstall(void);

#endif
