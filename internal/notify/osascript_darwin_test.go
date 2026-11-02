package notify

import "testing"

func TestOsascriptLine(t *testing.T) {
	got := osascriptLine("Nalmeok", `"따옴표" \ 전사 완료`)
	want := `display notification "\"따옴표\" \\ 전사 완료" with title "Nalmeok"`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
