#!/bin/sh
# Build assets/nalmeok.icns from assets/nalmeok.ico.
#
# The .ico holds one 256×256 image, so the 512 and 1024 sizes are upscaled.
# The generated .icns is committed; run this only when the source icon changes.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

SRC="assets/nalmeok.ico"
OUT="assets/nalmeok.icns"
SRC_PNG="build/icon-source.png"
ICONSET="build/icon.iconset"

rm -rf "$ICONSET" "$SRC_PNG"
mkdir -p "$ICONSET"

# sips reads the largest image stored in the ICO.
sips -s format png "$SRC" --out "$SRC_PNG" >/dev/null

for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$SRC_PNG" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$SRC_PNG" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done

iconutil -c icns "$ICONSET" -o "$OUT"
rm -rf "$ICONSET" "$SRC_PNG"
echo "만들었습니다: $OUT"
