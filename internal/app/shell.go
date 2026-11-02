package app

import "strings"

// explorerCmdLine builds the raw Windows command line for explorer.exe.
// With reveal it selects path in its folder (`/select,"<path>"`); otherwise
// explorer hands path to its default application.
//
// The line is set verbatim (SysProcAttr.CmdLine) because Go's own quoting
// wraps an argument with spaces in quotes as a whole — `"/select,C:\a b"` —
// and explorer then ignores /select. The quotes go around the path only.
// Windows paths cannot contain `"`, so any found are dropped rather than
// escaped.
func explorerCmdLine(path string, reveal bool) string {
	quoted := `"` + strings.ReplaceAll(path, `"`, "") + `"`
	if reveal {
		return `explorer.exe /select,` + quoted
	}
	return `explorer.exe ` + quoted
}
