#!/bin/bash
# Builds macos/build/Router.app. Set UNIVERSAL=1 for an arm64+x86_64 build (needs Xcode).
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo 0.1.0)}"
ARGS=(-c release)
if [[ "${UNIVERSAL:-0}" == 1 ]]; then
	ARGS+=(--arch arm64 --arch x86_64)
fi
swift build "${ARGS[@]}"
BIN="$(swift build "${ARGS[@]}" --show-bin-path)/RouterApp"
APP=build/Router.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/Router"
sed "s/__VERSION__/${VERSION#v}/g" Info.plist > "$APP/Contents/Info.plist"
codesign --force --sign - "$APP"
echo "Built $(pwd)/$APP"
