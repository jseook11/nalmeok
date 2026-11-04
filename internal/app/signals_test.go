package app

import (
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
)

func TestSignalsSettingIsSaved(t *testing.T) {
	ta := newTestApp(t)
	if !ta.app.SignalsEnabled() {
		t.Fatal("signals must start on")
	}
	if err := ta.app.SetSignalsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if ta.app.SignalsEnabled() {
		t.Fatal("SignalsEnabled = true after turning off")
	}
	reloaded, err := config.LoadFrom(ta.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SignalsEnabled() {
		t.Fatal("setting not saved to config.json")
	}
}

func TestFreeStatusWithoutClientIsUnknown(t *testing.T) {
	ta := newTestApp(t)
	st, err := ta.app.FreeStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Free.Level != "unknown" || st.Batch.Level != "unknown" {
		t.Fatalf("status = %+v", st)
	}
}
