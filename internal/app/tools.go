//go:build tools

package app

// Pins the Gemini SDK for the phase 2 runner. This file is only built with
// the "tools" tag, so it keeps the dependency in go.mod without linking it
// into the phase 1 binary.
import _ "google.golang.org/genai"
