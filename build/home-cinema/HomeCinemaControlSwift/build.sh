#!/bin/bash
set -euo pipefail
APP_NAME="Home Cinema Control"
BIN_NAME=HomeCinemaControl
SRC_DIR=$(cd "$(dirname "$0")" && pwd)
APP_DIR="$SRC_DIR/${APP_NAME}.app"
MACOS_DIR="$APP_DIR/Contents/MacOS"
RES_DIR="$APP_DIR/Contents/Resources"
PLIST_SRC="$SRC_DIR/Info.plist"
SWIFT_FILES=($SRC_DIR/HomeCinemaControlApp.swift $SRC_DIR/ContentView.swift)
SDK=$(xcrun --show-sdk-path --sdk macosx)
export SWIFT_MODULE_CACHE_PATH="$SRC_DIR/.swift-module-cache"
mkdir -p "$SWIFT_MODULE_CACHE_PATH"

# Sandbox-friendly Go caches (avoid ~/Library/Caches and ~/go outside workspace)
export GOCACHE="$SRC_DIR/.go-build-cache"
export GOPATH="$SRC_DIR/.go"
export GOMODCACHE="$GOPATH/pkg/mod"
mkdir -p "$GOCACHE" "$GOMODCACHE"

# Targets
SWIFT_TARGET_ARM64="arm64-apple-macos12"
SWIFT_TARGET_X86="x86_64-apple-macos12"

# Icon setup (choose first existing)
ICON_SRC="$SRC_DIR/icon.png"
ICON_NAME=HomeCinema
ICON_ICNS="$RES_DIR/${ICON_NAME}.icns"
ICONSET_BASE=$(mktemp -d /tmp/homecinema.iconset.XXXXXX)
ICONSET_DIR="${ICONSET_BASE}.iconset"
mv "$ICONSET_BASE" "$ICONSET_DIR"

# Build server binary (arm64 macOS) next to this script
echo "Building server binary (universal)..."
SERVER_SRC="$SRC_DIR/../../../main.go"
GOOS=darwin GOARCH=arm64 go build -o "$SRC_DIR/../HomeCinemaServer-arm64" "$SERVER_SRC"
GOOS=darwin GOARCH=amd64 go build -o "$SRC_DIR/../HomeCinemaServer-x86_64" "$SERVER_SRC"
lipo -create -output "$SRC_DIR/../HomeCinemaServer" "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"
rm -f "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR" "$RES_DIR"
cp "$PLIST_SRC" "$APP_DIR/Contents/Info.plist"

# Generate .icns if source PNG exists
if [ -n "$ICON_SRC" ]; then
  echo "Generating app icon from $ICON_SRC"
  sips -z 16 16     "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16.png" >/dev/null
  sips -z 32 32     "$ICON_SRC" --out "$ICONSET_DIR/icon_16x16@2x.png" >/dev/null
  sips -z 32 32     "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32.png" >/dev/null
  sips -z 64 64     "$ICON_SRC" --out "$ICONSET_DIR/icon_32x32@2x.png" >/dev/null
  sips -z 128 128   "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128.png" >/dev/null
  sips -z 256 256   "$ICON_SRC" --out "$ICONSET_DIR/icon_128x128@2x.png" >/dev/null
  sips -z 256 256   "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256.png" >/dev/null
  sips -z 512 512   "$ICON_SRC" --out "$ICONSET_DIR/icon_256x256@2x.png" >/dev/null
  sips -z 512 512   "$ICON_SRC" --out "$ICONSET_DIR/icon_512x512.png" >/dev/null
  cp "$ICON_SRC" "$ICONSET_DIR/icon_512x512@2x.png"
  iconutil -c icns "$ICONSET_DIR" -o "$ICON_ICNS"
else
  echo "Icon source not found, skipping icon generation."
fi

# Build SwiftUI app (universal)
echo "Building UI (universal)..."
swiftc -sdk "$SDK" -target "$SWIFT_TARGET_ARM64" \
  -o "$MACOS_DIR/$BIN_NAME-arm64" \
  -framework SwiftUI -framework Combine -framework AppKit \
  -Xlinker -rpath -Xlinker @executable_path/../Frameworks \
  -emit-executable "${SWIFT_FILES[@]}"

swiftc -sdk "$SDK" -target "$SWIFT_TARGET_X86" \
  -o "$MACOS_DIR/$BIN_NAME-x86_64" \
  -framework SwiftUI -framework Combine -framework AppKit \
  -Xlinker -rpath -Xlinker @executable_path/../Frameworks \
  -emit-executable "${SWIFT_FILES[@]}"

lipo -create -output "$MACOS_DIR/$BIN_NAME" "$MACOS_DIR/$BIN_NAME-arm64" "$MACOS_DIR/$BIN_NAME-x86_64"
rm -f "$MACOS_DIR/$BIN_NAME-arm64" "$MACOS_DIR/$BIN_NAME-x86_64"

# Embed server binary into the app bundle
cp "$SRC_DIR/../HomeCinemaServer" "$MACOS_DIR/HomeCinemaServer"

chmod +x "$MACOS_DIR/$BIN_NAME"
chmod +x "$MACOS_DIR/HomeCinemaServer"
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR"
fi

rm -rf "$ICONSET_DIR"

echo "Built $APP_DIR"
