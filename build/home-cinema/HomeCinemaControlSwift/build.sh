#!/bin/bash
set -euo pipefail
APP_NAME="Home Cinema"
BIN_NAME=HomeCinemaControl
SRC_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SRC_DIR/../../.." && pwd)
APP_DIR="$SRC_DIR/${APP_NAME}.app"
MACOS_DIR="$APP_DIR/Contents/MacOS"
RES_DIR="$APP_DIR/Contents/Resources"
SCRIPTS_DIR="$RES_DIR/scripts"
PLIST_SRC="$SRC_DIR/Info.plist"
SWIFT_FILES=(
  "$SRC_DIR/AppTheme.swift"
  "$SRC_DIR/ServerController.swift"
  "$SRC_DIR/ContentView.swift"
  "$SRC_DIR/HomeCinemaControlApp.swift"
)
SDK=$(xcrun --show-sdk-path --sdk macosx)
export SWIFT_MODULE_CACHE_PATH="$SRC_DIR/.swift-module-cache"
mkdir -p "$SWIFT_MODULE_CACHE_PATH"

# Sandbox-friendly clang module cache (avoid ~/.cache/clang/ModuleCache)
CLANG_MODULE_CACHE_PATH="$SRC_DIR/.clang-module-cache"
mkdir -p "$CLANG_MODULE_CACHE_PATH"

# Sandbox-friendly Go caches (avoid ~/Library/Caches and ~/go outside workspace)
export GOCACHE="$SRC_DIR/.go-build-cache"
mkdir -p "$GOCACHE"
if [ -d "$HOME/go/pkg/mod" ]; then
  export GOMODCACHE="$HOME/go/pkg/mod"
else
  export GOPATH="$SRC_DIR/.go"
  export GOMODCACHE="$GOPATH/pkg/mod"
  mkdir -p "$GOMODCACHE"
fi

# Targets
SWIFT_TARGET_ARM64="arm64-apple-macos12"
SWIFT_TARGET_X86="x86_64-apple-macos12"

# Icon setup
ICON_SRC="$SRC_DIR/icon.png"

# Build server binary (arm64 macOS) next to this script
echo "Building server binary (universal)..."
GOOS=darwin GOARCH=arm64 go build -o "$SRC_DIR/../HomeCinemaServer-arm64" "$REPO_ROOT"
GOOS=darwin GOARCH=amd64 go build -o "$SRC_DIR/../HomeCinemaServer-x86_64" "$REPO_ROOT"
lipo -create -output "$SRC_DIR/../HomeCinemaServer" "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"
rm -f "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR" "$RES_DIR" "$SCRIPTS_DIR"
cp "$PLIST_SRC" "$APP_DIR/Contents/Info.plist"

# Bundle the source PNG icon (used as app icon via Info.plist)
if [ -f "$ICON_SRC" ]; then
  cp "$ICON_SRC" "$RES_DIR/icon.png"
else
  echo "Icon source not found, skipping icon copy."
fi

# Build SwiftUI app (universal)
echo "Building UI (universal)..."
swiftc -sdk "$SDK" -target "$SWIFT_TARGET_ARM64" \
  -module-cache-path "$SWIFT_MODULE_CACHE_PATH" \
  -Xcc -fmodules-cache-path="$CLANG_MODULE_CACHE_PATH" \
  -o "$MACOS_DIR/$BIN_NAME-arm64" \
  -framework SwiftUI -framework Combine -framework AppKit \
  -Xlinker -rpath -Xlinker @executable_path/../Frameworks \
  -emit-executable "${SWIFT_FILES[@]}"

swiftc -sdk "$SDK" -target "$SWIFT_TARGET_X86" \
  -module-cache-path "$SWIFT_MODULE_CACHE_PATH" \
  -Xcc -fmodules-cache-path="$CLANG_MODULE_CACHE_PATH" \
  -o "$MACOS_DIR/$BIN_NAME-x86_64" \
  -framework SwiftUI -framework Combine -framework AppKit \
  -Xlinker -rpath -Xlinker @executable_path/../Frameworks \
  -emit-executable "${SWIFT_FILES[@]}"

UI_FAT_TMP=$(mktemp -t homecinema.ui.XXXXXX)
lipo -create -output "$UI_FAT_TMP" "$MACOS_DIR/$BIN_NAME-arm64" "$MACOS_DIR/$BIN_NAME-x86_64"
mv -f "$UI_FAT_TMP" "$MACOS_DIR/$BIN_NAME"
rm -f "$MACOS_DIR/$BIN_NAME-arm64" "$MACOS_DIR/$BIN_NAME-x86_64"

# Embed server binary into the app bundle
cp "$SRC_DIR/../HomeCinemaServer" "$MACOS_DIR/HomeCinemaServer"
cp "$SRC_DIR/scripts/"*.sh "$SCRIPTS_DIR/"

chmod +x "$MACOS_DIR/$BIN_NAME"
chmod +x "$MACOS_DIR/HomeCinemaServer"
chmod +x "$SCRIPTS_DIR/"*.sh
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR"
fi

echo "Built $APP_DIR"
