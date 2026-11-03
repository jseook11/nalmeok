package app

import (
	"context"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Events the menu bar sends to the window.
const (
	EventMenuSettings = "menu:settings"
	EventMenuHelp     = "menu:help"
)

// Menu is the macOS menu bar: app, Edit, Window and Help. Windows has no menu
// bar, so main sets it on macOS only.
//
// The app menu is built here rather than with menu.AppMenu(), which takes no
// extra items, so it can hold Settings (⌘,). Close (⌘W) quits, the same as
// the window's close button: both go through Wails' Quit and so through
// BeforeClose, which asks first while a free item is being transcribed.
func (a *App) Menu() *menu.Menu {
	bar := menu.NewMenu()

	appMenu := bar.AddSubmenu("Nalmeok")
	appMenu.AddText(i18n.T("menubar.settings"), keys.CmdOrCtrl(","), a.emitMenu(EventMenuSettings))
	appMenu.AddSeparator()
	appMenu.AddText(i18n.T("menubar.hide"), keys.CmdOrCtrl("h"), a.withContext(wailsruntime.Hide))
	appMenu.AddSeparator()
	appMenu.AddText(i18n.T("menubar.quit"), keys.CmdOrCtrl("q"), a.withContext(wailsruntime.Quit))

	bar.Append(menu.EditMenu())

	window := bar.AddSubmenu(i18n.T("menubar.window"))
	window.AddText(i18n.T("menubar.minimize"), keys.CmdOrCtrl("m"), a.withContext(wailsruntime.WindowMinimise))
	window.AddText(i18n.T("menubar.close"), keys.CmdOrCtrl("w"), a.withContext(wailsruntime.Quit))

	help := bar.AddSubmenu(i18n.T("help.title"))
	help.AddText(i18n.T("menubar.help"), nil, a.emitMenu(EventMenuHelp))
	return bar
}

// withContext runs fn with the window's context once the window exists.
func (a *App) withContext(fn func(context.Context)) menu.Callback {
	return func(*menu.CallbackData) {
		if ctx := a.context(); ctx != nil {
			fn(ctx)
		}
	}
}

// emitMenu sends a menu event to the window.
func (a *App) emitMenu(event string) menu.Callback {
	return a.withContext(func(ctx context.Context) {
		wailsruntime.EventsEmit(ctx, event)
	})
}
