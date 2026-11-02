package app

import "testing"

func TestExplorerCmdLine(t *testing.T) {
	cases := []struct {
		path   string
		reveal bool
		want   string
	}{
		{`C:\Users\me\강의 전사\1주차 녹음.md`, true, `explorer.exe /select,"C:\Users\me\강의 전사\1주차 녹음.md"`},
		{`C:\a\b.md`, true, `explorer.exe /select,"C:\a\b.md"`},
		{`C:\Users\me\강의 전사\1주차 녹음.md`, false, `explorer.exe "C:\Users\me\강의 전사\1주차 녹음.md"`},
		{`C:\odd"name.md`, true, `explorer.exe /select,"C:\oddname.md"`},
		{`\\server\share\a & b.md`, false, `explorer.exe "\\server\share\a & b.md"`},
	}
	for _, c := range cases {
		if got := explorerCmdLine(c.path, c.reveal); got != c.want {
			t.Errorf("explorerCmdLine(%q, %v) = %q, want %q", c.path, c.reveal, got, c.want)
		}
	}
}
