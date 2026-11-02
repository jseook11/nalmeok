#!/bin/sh
# Build the macOS .app bundle without the Wails CLI.
#
# Optional signing/notarization:
#   NALMEOK_SIGN_IDENTITY="Developer ID Application: ..." \
#   NALMEOK_NOTARY_PROFILE="nalmeok" ./scripts/build_mac_app.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION="${VERSION:-0.7.0}"
BIN="build/nalmeok"
APP="build/날먹.app"
CONTENTS="$APP/Contents"

mkdir -p build
go build -tags desktop,production -ldflags "-s -w" -o "$BIN" ./cmd/nalmeok

rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
cp "$BIN" "$CONTENTS/MacOS/nalmeok"

cat > "$CONTENTS/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>날먹</string>
	<key>CFBundleDisplayName</key>
	<string>날먹</string>
	<key>CFBundleIdentifier</key>
	<string>kr.nalmeok.app</string>
	<key>CFBundleExecutable</key>
	<string>nalmeok</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$VERSION</string>
	<key>CFBundleVersion</key>
	<string>$VERSION</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.productivity</string>
</dict>
</plist>
PLIST

if [ -n "${NALMEOK_SIGN_IDENTITY:-}" ]; then
	codesign --force --options runtime --timestamp \
		--sign "$NALMEOK_SIGN_IDENTITY" "$APP"
	echo "서명했습니다: $APP"

	if [ -n "${NALMEOK_NOTARY_PROFILE:-}" ]; then
		ZIP="build/날먹.zip"
		rm -f "$ZIP"
		ditto -c -k --keepParent "$APP" "$ZIP"
		xcrun notarytool submit "$ZIP" \
			--keychain-profile "$NALMEOK_NOTARY_PROFILE" --wait
		xcrun stapler staple "$APP"
		rm -f "$ZIP"
		echo "공증과 스테이플링을 마쳤습니다: $APP"
	fi
else
	echo "서명하지 않은 앱입니다: $APP"
fi

echo "빌드 완료: $APP"
