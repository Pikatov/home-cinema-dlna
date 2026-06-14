#!/bin/bash
set -euo pipefail
APP_NAME="Home Cinema"
BIN_NAME=HomeCinemaControl
SRC_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SRC_DIR/../../.." && pwd)
APP_DIR="$SRC_DIR/${APP_NAME}.app"
MACOS_DIR="$APP_DIR/Contents/MacOS"
RES_DIR="$APP_DIR/Contents/Resources"
FRAMEWORKS_DIR="$APP_DIR/Contents/Frameworks"
SCRIPTS_DIR="$RES_DIR/scripts"
PLIST_SRC="$SRC_DIR/Info.plist"
SWIFT_FILES=(
  "$SRC_DIR/Sources/HomeCinemaControlSwift/AppTheme.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/ThemePreference.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/GlassPanel.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/ServerController.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/ContentView.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/WindowChromeConfigurator.swift"
  "$SRC_DIR/Sources/HomeCinemaControlSwift/HomeCinemaControlApp.swift"
)
SDK=$(xcrun --show-sdk-path --sdk macosx)
export SWIFT_MODULE_CACHE_PATH=$(mktemp -d "${TMPDIR:-/tmp}/homecinema.swift-module-cache.XXXXXX")

# Sandbox-friendly clang module cache (avoid ~/.cache/clang/ModuleCache)
CLANG_MODULE_CACHE_PATH=$(mktemp -d "${TMPDIR:-/tmp}/homecinema.clang-module-cache.XXXXXX")
trap 'rm -rf "$SWIFT_MODULE_CACHE_PATH" "$CLANG_MODULE_CACHE_PATH"' EXIT

# Sandbox-friendly Go caches (avoid ~/Library/Caches and ~/go outside workspace)
export GOCACHE="$SRC_DIR/.go-build-cache"
mkdir -p "$GOCACHE"
export GOPATH="$SRC_DIR/.go"
export GOMODCACHE="$GOPATH/pkg/mod"
mkdir -p "$GOMODCACHE"

# Targets — min macOS 14 (Sonoma) для Observation framework (@Observable) и
# scenePhase-aware UI. См. Package.swift / Info.plist.
SWIFT_TARGET_ARM64="arm64-apple-macos14"
SWIFT_TARGET_X86="x86_64-apple-macos14"

# Icon setup
ICON_SRC="$SRC_DIR/icon.png"

# Build server binary (arm64 macOS) next to this script
echo "Building server binary (universal)..."
GOOS=darwin GOARCH=arm64 go build -o "$SRC_DIR/../HomeCinemaServer-arm64" "$REPO_ROOT"
GOOS=darwin GOARCH=amd64 go build -o "$SRC_DIR/../HomeCinemaServer-x86_64" "$REPO_ROOT"
lipo -create -output "$SRC_DIR/../HomeCinemaServer" "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"
rm -f "$SRC_DIR/../HomeCinemaServer-arm64" "$SRC_DIR/../HomeCinemaServer-x86_64"

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR" "$RES_DIR" "$FRAMEWORKS_DIR" "$SCRIPTS_DIR"
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

copy_tool_with_homebrew_dylibs() {
  local tool_name="$1"
  local tool_path
  tool_path=$(command -v "$tool_name" || true)
  if [[ -z "$tool_path" ]]; then
    echo "$tool_name not found, skipping bundled $tool_name."
    return 0
  fi

  local bundled_tool="$MACOS_DIR/$tool_name"
  cp "$tool_path" "$bundled_tool"
  chmod u+w "$bundled_tool"

  local deps_file queue_file
  deps_file=$(mktemp "${TMPDIR:-/tmp}/homecinema.${tool_name}.deps.XXXXXX")
  queue_file=$(mktemp "${TMPDIR:-/tmp}/homecinema.${tool_name}.queue.XXXXXX")

  otool -L "$tool_path" | awk 'NR > 1 { print $1 }' | grep '^/opt/homebrew/' > "$queue_file" || true
  while IFS= read -r dep; do
    [[ -z "$dep" ]] && continue
    if grep -Fxq "$dep" "$deps_file" 2>/dev/null; then
      continue
    fi
    printf '%s\n' "$dep" >> "$deps_file"
    otool -L "$dep" | awk 'NR > 1 { print $1 }' | grep '^/opt/homebrew/' >> "$queue_file" || true
  done < "$queue_file"

  while IFS= read -r dep; do
    [[ -z "$dep" ]] && continue
    cp -L "$dep" "$FRAMEWORKS_DIR/$(basename "$dep")"
    chmod u+w "$FRAMEWORKS_DIR/$(basename "$dep")"
  done < "$deps_file"

  while IFS= read -r dep; do
    [[ -z "$dep" ]] && continue
    install_name_tool -change "$dep" "@executable_path/../Frameworks/$(basename "$dep")" "$bundled_tool" 2>/dev/null || true
  done < "$deps_file"

  while IFS= read -r copied; do
    [[ -f "$copied" ]] || continue
    local copied_base
    copied_base=$(basename "$copied")
    install_name_tool -id "@rpath/$copied_base" "$copied" 2>/dev/null || true
    while IFS= read -r dep; do
      [[ -z "$dep" ]] && continue
      install_name_tool -change "$dep" "@loader_path/$(basename "$dep")" "$copied" 2>/dev/null || true
    done < "$deps_file"
  done < <(find "$FRAMEWORKS_DIR" -type f -name '*.dylib' -print)

  rm -f "$deps_file" "$queue_file"
}

echo "Bundling ffmpeg/ffprobe..."
copy_tool_with_homebrew_dylibs ffmpeg
copy_tool_with_homebrew_dylibs ffprobe

chmod +x "$MACOS_DIR/$BIN_NAME"
chmod +x "$MACOS_DIR/HomeCinemaServer"
if [[ -f "$MACOS_DIR/ffmpeg" ]]; then chmod +x "$MACOS_DIR/ffmpeg"; fi
if [[ -f "$MACOS_DIR/ffprobe" ]]; then chmod +x "$MACOS_DIR/ffprobe"; fi
chmod +x "$SCRIPTS_DIR/"*.sh
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR"
fi

echo "Built $APP_DIR"
