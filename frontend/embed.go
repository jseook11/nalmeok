// Package frontend embeds the static UI served by Wails.
package frontend

import "embed"

// Assets contains the contents of frontend/dist, including the dist directory
// itself. Callers should fs.Sub(Assets, "dist") before handing it to Wails.
//
//go:embed all:dist
var Assets embed.FS
