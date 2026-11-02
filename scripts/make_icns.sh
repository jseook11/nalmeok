#!/bin/sh
# Build assets/nalmeok.icns from assets/nalmeok.png (1024×1024, macOS grid,
# already masked). The master PNG and the Windows .ico come from the full-bleed
# artwork assets/nalmeok-art.png:
#   uv run --no-project --with pillow python -I scripts/make_icon_master.py \
#     assets/nalmeok-art.png assets/nalmeok.png assets/nalmeok.ico
# All generated files are committed; run this only when the artwork changes.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

SRC="assets/nalmeok.png"
OUT="assets/nalmeok.icns"
ICONSET="build/icon.iconset"

rm -rf "$ICONSET"
mkdir -p "$ICONSET"

for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$SRC" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$SRC" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done

iconutil -c icns "$ICONSET" -o "$OUT"
rm -rf "$ICONSET"
echo "만들었습니다: $OUT"
