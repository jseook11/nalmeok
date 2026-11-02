// Package assets embeds the app icon for code that needs it at run time.
// Only the Windows notification imports it (the toast icon is a file).
package assets

import _ "embed"

// ICO is assets/nalmeok.ico, the same icon the exe carries.
//
//go:embed nalmeok.ico
var ICO []byte
