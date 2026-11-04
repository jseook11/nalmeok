#!/bin/sh
# Build the macOS .app bundle without the Wails CLI.
#
# The bundle carries a universal (arm64 + amd64) binary. build/nalmeok stays a
# native binary for development.
#
# Optional signing/notarization:
#   NALMEOK_SIGN_IDENTITY="Developer ID Application: ..." \
#   NALMEOK_NOTARY_PROFILE="nalmeok-notary" ./scripts/build_mac_app.sh
#
# With both credentials set, the notarized and stapled .app is also written to
# the release archive:
#   dist/Nalmeok-<version>-macos-universal.zip
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION="${VERSION:-0.10.2}"
BIN="build/nalmeok"
UNIVERSAL="build/nalmeok-universal"
APP="build/Nalmeok.app"
CONTENTS="$APP/Contents"
ICNS="assets/nalmeok.icns"
LDFLAGS="-s -w -X main.version=$VERSION"
NOTARIZED=0

if [ ! -f "$ICNS" ]; then
	echo "아이콘이 없어요: $ICNS (scripts/make_icns.sh로 만드세요)" >&2
	exit 1
fi

mkdir -p build
# -o is required: without it the binary is named `nalmeok` in the repository
# root instead of build/.
go build -tags desktop,production -ldflags "$LDFLAGS" -o "$BIN" ./cmd/nalmeok

# Wails' macOS backend uses cgo, so cross-building needs clang's -arch both for
# compiling and linking. Each slice is built on its own and joined with lipo.
# Without a deployment target clang stamps the build Mac's own macOS version
# into the binary (minos), and older Macs refuse to open the app and ask for a
# software update. 13.0 is the oldest macOS Go itself supports.
MIN_MACOS=13.0
for arch in arm64 amd64; do
	carch=$arch
	[ "$arch" = amd64 ] && carch=x86_64   # clang names it x86_64
	env CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
		MACOSX_DEPLOYMENT_TARGET="$MIN_MACOS" \
		CC="clang -arch $carch -mmacosx-version-min=$MIN_MACOS" \
		CGO_CFLAGS="-arch $carch -mmacosx-version-min=$MIN_MACOS" \
		CGO_LDFLAGS="-arch $carch -mmacosx-version-min=$MIN_MACOS" \
		go build -tags desktop,production -ldflags "$LDFLAGS" \
		-o "build/nalmeok-$arch" ./cmd/nalmeok
done
lipo -create build/nalmeok-arm64 build/nalmeok-amd64 -output "$UNIVERSAL"
rm -f build/nalmeok-arm64 build/nalmeok-amd64
echo "아키텍처: $(lipo -archs "$UNIVERSAL")"

rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
cp "$UNIVERSAL" "$CONTENTS/MacOS/nalmeok"
cp "$ICNS" "$CONTENTS/Resources/nalmeok.icns"

cat > "$CONTENTS/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Nalmeok</string>
	<key>CFBundleDisplayName</key>
	<string>Nalmeok</string>
	<key>CFBundleIdentifier</key>
	<string>kr.nalmeok.app</string>
	<key>CFBundleExecutable</key>
	<string>nalmeok</string>
	<key>CFBundleIconFile</key>
	<string>nalmeok.icns</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$VERSION</string>
	<key>CFBundleVersion</key>
	<string>$VERSION</string>
	<key>LSMinimumSystemVersion</key>
	<string>$MIN_MACOS</string>
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
		ZIP="build/Nalmeok.zip"
		rm -f "$ZIP"
		ditto -c -k --keepParent "$APP" "$ZIP"
		xcrun notarytool submit "$ZIP" \
			--keychain-profile "$NALMEOK_NOTARY_PROFILE" --wait
		xcrun stapler staple "$APP"
		rm -f "$ZIP"
		NOTARIZED=1
		echo "공증과 스테이플링을 마쳤습니다: $APP"
	fi
else
	# Ad-hoc signature: binds Info.plist so the bundle identifier
	# (kr.nalmeok.app) is the app's identity, which macOS notifications use.
	codesign --force --sign - "$APP"
	echo "임시 서명(ad-hoc)만 한 앱입니다: $APP"
fi

if [ "$NOTARIZED" = 1 ]; then
	mkdir -p dist
	DIST_ZIP="dist/Nalmeok-$VERSION-macos-universal.zip"
	rm -f "$DIST_ZIP"
	# ditto keeps the bundle's signature, extended attributes and the stapled
	# notarization ticket; plain zip can break them.
	ditto -c -k --keepParent "$APP" "$DIST_ZIP"
	echo "배포용 ZIP: $DIST_ZIP"

	# Keep the checksum file in step with a rebuilt ZIP; other lines (Windows
	# executables built by build_windows_app.sh) are left as they are.
	CHECKSUMS="dist/SHA256SUMS.txt"
	if [ -f "$CHECKSUMS" ]; then
		sed "/  Nalmeok-$VERSION-macos-universal.zip\$/d" "$CHECKSUMS" > "$CHECKSUMS.tmp"
	else
		: > "$CHECKSUMS.tmp"
	fi
	shasum -a 256 "$DIST_ZIP" | sed 's#  dist/#  #' >> "$CHECKSUMS.tmp"
	mv "$CHECKSUMS.tmp" "$CHECKSUMS"
	echo "체크섬 갱신: $CHECKSUMS"
else
	echo "공증까지 마치지 않은 빌드라 dist에 배포용 ZIP을 만들지 않았습니다."
fi

echo "빌드 완료: $APP"
