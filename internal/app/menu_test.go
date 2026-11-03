package app

import (
	"testing"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

func TestMenuBar(t *testing.T) {
	bar := (&App{}).Menu()
	if len(bar.Items) != 4 {
		t.Fatalf("menu bar has %d menus, want 4", len(bar.Items))
	}
	if bar.Items[1].Role != menu.EditMenuRole {
		t.Errorf("second menu role = %v, want Edit", bar.Items[1].Role)
	}

	// accel finds a text item by label in a submenu and returns its shortcut.
	accel := func(top int, label string) *keys.Accelerator {
		t.Helper()
		sub := bar.Items[top].SubMenu
		if sub == nil {
			t.Fatalf("menu %d has no submenu", top)
		}
		for _, item := range sub.Items {
			if item.Label == label {
				if item.Click == nil {
					t.Errorf("%q has no action", label)
				}
				return item.Accelerator
			}
		}
		t.Fatalf("menu %d has no %q", top, label)
		return nil
	}
	for _, c := range []struct {
		top   int
		label string
		key   string
	}{
		{0, i18n.T("menubar.settings"), ","},
		{0, i18n.T("menubar.hide"), "h"},
		{0, i18n.T("menubar.quit"), "q"},
		{2, i18n.T("menubar.minimize"), "m"},
		{2, i18n.T("menubar.close"), "w"},
	} {
		got := accel(c.top, c.label)
		if got == nil || got.Key != c.key || len(got.Modifiers) != 1 || got.Modifiers[0] != keys.CmdOrCtrlKey {
			t.Errorf("%q accelerator = %+v, want ⌘%s", c.label, got, c.key)
		}
	}
	if bar.Items[3].Label != i18n.T("help.title") {
		t.Errorf("last menu = %q, want help", bar.Items[3].Label)
	}
	accel(3, i18n.T("menubar.help"))

	// Before the window exists the actions do nothing rather than panic.
	bar.Items[0].SubMenu.Items[0].Click(nil)
}
