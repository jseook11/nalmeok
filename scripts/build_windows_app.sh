#!/bin/sh
# Build the Windows executables from macOS or Linux.
#
# Wails' Windows backend is pure Go, so a plain cross compile works (CGO off).
# The Windows icon and application manifest are not embedded yet; that waits on
# a tool decision (docs/handoff-2026-11-02.md §4).
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION="${VERSION:-0.7.0}"
OUT="build/windows"
LDFLAGS="-s -w -H windowsgui -X main.version=$VERSION"

mkdir -p "$OUT"

# amd64 is the main download; arm64 ships next to it. -H windowsgui keeps the
# console window hidden when the app starts.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
	go build -tags desktop,production -ldflags "$LDFLAGS" \
	-o "$OUT/Nalmeok.exe" ./cmd/nalmeok

GOOS=windows GOARCH=arm64 CGO_ENABLED=0 \
	go build -tags desktop,production -ldflags "$LDFLAGS" \
	-o "$OUT/Nalmeok-arm64.exe" ./cmd/nalmeok

echo "빌드 완료: $OUT/Nalmeok.exe, $OUT/Nalmeok-arm64.exe"
