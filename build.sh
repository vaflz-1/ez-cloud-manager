#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="EZ Cloud Manager.app"
APP_PATH="/Users/octavian/Applications/$APP_NAME"
OLD_APP_PATH="/Users/octavian/Applications/Cloud EZ Manager.app"
LEGACY_APP_PATH="/Users/octavian/Applications/AWS Profile Manager.app"
BUILD_ROOT="${TMPDIR:-/tmp}/ez-cloud-manager-build"
BUILD_APP="$BUILD_ROOT/$APP_NAME"
DIST_DIR="$PROJECT_DIR/dist"
CLI_LINK="/Users/octavian/.local/bin/ezcloud"
OLD_CLI_LINK="/Users/octavian/.local/bin/cloudez"
LEGACY_CLI_LINK="/Users/octavian/.local/bin/awspm"
ICON_FILE="$PROJECT_DIR/assets/EZCloudManagerCloudGear.icns"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

pkill -x EZCloudManager 2>/dev/null || true
pkill -x CloudEZManager 2>/dev/null || true
pkill -x AWSProfileManager 2>/dev/null || true

rm -rf "$BUILD_ROOT"
mkdir -p "$BUILD_APP/Contents/MacOS" "$BUILD_APP/Contents/Resources" "$DIST_DIR" "$(dirname "$CLI_LINK")" "/Users/octavian/Applications"

go build -trimpath -o "$DIST_DIR/ezcloud" "$PROJECT_DIR/cmd/ezcloud"

# Compile every Swift source in ui/ into one binary (app was split from a
# single God-object file into focused modules; order does not matter to swiftc).
swiftc "$PROJECT_DIR"/ui/*.swift \
  -o "$BUILD_APP/Contents/MacOS/EZCloudManager" \
  -framework AppKit

cp "$PROJECT_DIR/Info.plist" "$BUILD_APP/Contents/Info.plist"
cp "$ICON_FILE" "$BUILD_APP/Contents/Resources/EZCloudManagerCloudGear.icns"
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

rm -rf "$APP_PATH" "$OLD_APP_PATH" "$LEGACY_APP_PATH"
mv "$BUILD_APP" "$APP_PATH"
touch "$APP_PATH"
if [ -x "$LSREGISTER" ]; then
  "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1 || true
fi

rm -f "$CLI_LINK" "$OLD_CLI_LINK" "$LEGACY_CLI_LINK"
ln -s "$DIST_DIR/ezcloud" "$CLI_LINK"
ln -s "$DIST_DIR/ezcloud" "$OLD_CLI_LINK"
ln -s "$DIST_DIR/ezcloud" "$LEGACY_CLI_LINK"

echo "Installed:"
echo "  $APP_PATH"
echo "  $CLI_LINK -> $DIST_DIR/ezcloud"
echo "  $OLD_CLI_LINK -> $DIST_DIR/ezcloud"
echo "  $LEGACY_CLI_LINK -> $DIST_DIR/ezcloud"
