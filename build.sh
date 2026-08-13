#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
USER_HOME="${HOME:?HOME is not set}"
APP_NAME="Kervik.app"
# Install system-wide so the app shows up in Finder → Applications, Launchpad
# and Spotlight. Falls back to ~/Applications if /Applications is not writable.
APP_DIR="/Applications"
if [ ! -w "$APP_DIR" ]; then
  APP_DIR="$USER_HOME/Applications"
  echo "warning: /Applications not writable — installing to $APP_DIR instead"
fi
APP_PATH="$APP_DIR/$APP_NAME"
HOME_APP_PATH="$USER_HOME/Applications/$APP_NAME"
PREVIOUS_APP_PATH="$APP_DIR/EZ Cloud Manager.app"
HOME_PREVIOUS_APP_PATH="$USER_HOME/Applications/EZ Cloud Manager.app"
OLD_APP_PATH="$USER_HOME/Applications/Cloud EZ Manager.app"
LEGACY_APP_PATH="$USER_HOME/Applications/AWS Profile Manager.app"
BUILD_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kervik-build.XXXXXX")"
BUILD_APP="$BUILD_ROOT/$APP_NAME"
DIST_DIR="$PROJECT_DIR/dist"
CLI_LINK="$USER_HOME/.local/bin/ezcloud"
PRODUCT_CLI_LINK="$USER_HOME/.local/bin/kervik"
OLD_CLI_LINK="$USER_HOME/.local/bin/cloudez"
LEGACY_CLI_LINK="$USER_HOME/.local/bin/awspm"
INSTALLED_CLI_PATH="$APP_PATH/Contents/Resources/ezcloud"
ICON_BASENAME="EZCloudManagerAppIcon"
ICON_FILE="$PROJECT_DIR/assets/$ICON_BASENAME.icns"
SWIFT_TARGET="${EZCLOUD_SWIFT_TARGET:-$(uname -m)-apple-macosx13.0}"
GO_BIN="${EZCLOUD_GO_BIN:-}"
if [ -z "$GO_BIN" ]; then
  if [ -x /opt/homebrew/bin/go ]; then
    GO_BIN=/opt/homebrew/bin/go
  else
    GO_BIN="$(command -v go || true)"
  fi
fi
if [ -z "$GO_BIN" ] || [ ! -x "$GO_BIN" ]; then
  echo "error: Go toolchain not found; set EZCLOUD_GO_BIN to an absolute executable path" >&2
  exit 1
fi
SWIFT_PATH_FLAGS=(-file-prefix-map "$PROJECT_DIR=." -debug-prefix-map "$PROJECT_DIR=.")
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

pkill -x EZCloudManager 2>/dev/null || true
pkill -x Kervik 2>/dev/null || true
pkill -x CloudEZManager 2>/dev/null || true
pkill -x AWSProfileManager 2>/dev/null || true

trap 'rm -rf "$BUILD_ROOT"' EXIT
mkdir -p "$BUILD_APP/Contents/MacOS" "$BUILD_APP/Contents/Resources" "$DIST_DIR" "$(dirname "$CLI_LINK")" "$USER_HOME/Applications"

# Generate every icon representation from the committed master unless the
# current Xcode iconutil rejects a previously verified legacy iconset. The
# explicit opt-out still validates that the committed ICNS can be unpacked;
# it never silently accepts a missing or corrupt resource.
if [ "${EZCLOUD_REGENERATE_ICON:-1}" = "1" ]; then
  swift "$PROJECT_DIR/tools/generate-icon.swift"
else
  ICON_VERIFY_DIR="$BUILD_ROOT/icon-verify.iconset"
  iconutil -c iconset "$ICON_FILE" -o "$ICON_VERIFY_DIR"
  [ "$(find "$ICON_VERIFY_DIR" -type f | wc -l | tr -d ' ')" = "10" ] || {
    echo "error: committed icon does not contain all 10 representations" >&2
    exit 1
  }
  echo "Using validated committed icon (EZCLOUD_REGENERATE_ICON=0)"
fi

if [ "${EZCLOUD_BUILD_MODE:-release}" = "debug" ]; then
  "$GO_BIN" build -trimpath -o "$DIST_DIR/ezcloud" "$PROJECT_DIR/cmd/ezcloud"
  swiftc -target "$SWIFT_TARGET" "${SWIFT_PATH_FLAGS[@]}" "$PROJECT_DIR"/ui/*.swift \
    -o "$BUILD_APP/Contents/MacOS/EZCloudManager" \
    -framework AppKit
else
  "$GO_BIN" build -trimpath -ldflags="-s -w" -o "$DIST_DIR/ezcloud" "$PROJECT_DIR/cmd/ezcloud"
  swiftc -target "$SWIFT_TARGET" -O -whole-module-optimization "${SWIFT_PATH_FLAGS[@]}" "$PROJECT_DIR"/ui/*.swift \
    -o "$BUILD_APP/Contents/MacOS/EZCloudManager" \
    -framework AppKit
fi

cp "$PROJECT_DIR/Info.plist" "$BUILD_APP/Contents/Info.plist"
cp "$ICON_FILE" "$BUILD_APP/Contents/Resources/$ICON_BASENAME.icns"
cp "$DIST_DIR/ezcloud" "$BUILD_APP/Contents/Resources/ezcloud"
chmod +x "$BUILD_APP/Contents/MacOS/EZCloudManager" "$BUILD_APP/Contents/Resources/ezcloud"

# Sign with the Hardened Runtime (ad-hoc), inside-out: the bundled CLI first,
# then the app bundle with entitlements, so nested code is validly signed.
# (Notarization additionally requires a Developer ID cert + `notarytool`.)
ENTITLEMENTS="$PROJECT_DIR/EZCloudManager.entitlements"
codesign --force --options runtime --sign - "$BUILD_APP/Contents/Resources/ezcloud" >/dev/null
codesign --force --options runtime --entitlements "$ENTITLEMENTS" --sign - "$BUILD_APP" >/dev/null
codesign --verify --deep --strict --verbose=2 "$BUILD_APP"
codesign --display --entitlements - "$BUILD_APP" 2>/dev/null | grep -q get-task-allow && echo "hardened runtime + entitlements applied"

rm -rf "$APP_PATH" "$PREVIOUS_APP_PATH" "$HOME_PREVIOUS_APP_PATH" "$OLD_APP_PATH" "$LEGACY_APP_PATH"
# Drop a stale copy in ~/Applications when installing system-wide, so Spotlight
# never launches an outdated build.
if [ "$APP_PATH" != "$HOME_APP_PATH" ]; then
  rm -rf "$HOME_APP_PATH"
fi
mv "$BUILD_APP" "$APP_PATH"
touch "$APP_PATH"
if [ -x "$LSREGISTER" ]; then
  "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1 || true
fi

rm -f "$CLI_LINK" "$PRODUCT_CLI_LINK" "$OLD_CLI_LINK" "$LEGACY_CLI_LINK"
ln -s "$INSTALLED_CLI_PATH" "$CLI_LINK"
ln -s "$INSTALLED_CLI_PATH" "$PRODUCT_CLI_LINK"
ln -s "$INSTALLED_CLI_PATH" "$OLD_CLI_LINK"
ln -s "$INSTALLED_CLI_PATH" "$LEGACY_CLI_LINK"

echo "Installed:"
echo "  $APP_PATH"
echo "  $CLI_LINK -> $INSTALLED_CLI_PATH"
echo "  $PRODUCT_CLI_LINK -> $INSTALLED_CLI_PATH"
echo "  $OLD_CLI_LINK -> $INSTALLED_CLI_PATH"
echo "  $LEGACY_CLI_LINK -> $INSTALLED_CLI_PATH"
