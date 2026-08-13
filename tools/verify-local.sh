#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kervik-verify.XXXXXX")"
trap 'rm -rf "$VERIFY_ROOT"' EXIT

export GOCACHE="$VERIFY_ROOT/go-cache"
export GOTMPDIR="$VERIFY_ROOT/go-tmp"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$VERIFY_ROOT/swift-cache"

cd "$PROJECT_DIR"

echo "[1/9] Go tests"
go test -count=1 ./...

echo "[2/9] Go race detector"
go test -race -count=1 ./...

echo "[3/9] Go vet"
go vet ./...

echo "[4/9] Swift macOS 13 typecheck"
swiftc -target "$(uname -m)-apple-macosx13.0" -warnings-as-errors \
  -file-prefix-map "$PROJECT_DIR=." -debug-prefix-map "$PROJECT_DIR=." \
  -module-cache-path "$VERIFY_ROOT/swift-cache" \
  -typecheck ui/*.swift -framework AppKit

echo "[5/9] Connection scope policy smoke"
swiftc -O -whole-module-optimization \
  -target "$(uname -m)-apple-macosx13.0" \
  -module-cache-path "$VERIFY_ROOT/swift-cache" \
  tools/ConnectionScopePolicySmoke.swift ui/Models.swift \
  -o "$VERIFY_ROOT/ConnectionScopePolicySmoke"
"$VERIFY_ROOT/ConnectionScopePolicySmoke"

echo "[6/9] Metadata and shell validation"
plutil -lint Info.plist EZCloudManager.entitlements
bash -n build.sh
git diff --check

echo "[7/9] Deterministic icon"
ICON_CHECK_ROOT="$VERIFY_ROOT/icon-check"
mkdir -p "$ICON_CHECK_ROOT/tools" "$ICON_CHECK_ROOT/assets"
cp tools/generate-icon.swift "$ICON_CHECK_ROOT/tools/generate-icon.swift"
cp assets/EZCloudManagerAppIcon.master.png \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.master.png"
EZCLOUD_SKIP_ICONUTIL=1 swift -module-cache-path "$VERIFY_ROOT/swift-cache" \
  "$ICON_CHECK_ROOT/tools/generate-icon.swift"
cmp assets/EZCloudManagerAppIcon.preview.png \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.preview.png"
diff -rq assets/EZCloudManagerAppIcon.iconset \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.iconset"
COMMITTED_ICONSET="$VERIFY_ROOT/committed-icon.iconset"
iconutil -c iconset assets/EZCloudManagerAppIcon.icns -o "$COMMITTED_ICONSET"
[ "$(find "$COMMITTED_ICONSET" -type f | wc -l | tr -d ' ')" = "10" ] || {
  echo "error: committed ICNS does not contain all 10 representations" >&2
  exit 1
}

echo "[8/9] Local secret scan"
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks dir . --redact --exit-code 1
  gitleaks git . --redact --exit-code 1
else
  echo "warning: gitleaks is not installed; secret scan skipped" >&2
fi

echo "[9/9] Process-boundary smoke"
go build -trimpath -o "$VERIFY_ROOT/ezcloud" ./cmd/ezcloud
swiftc -O -whole-module-optimization \
  -target "$(uname -m)-apple-macosx13.0" \
  -module-cache-path "$VERIFY_ROOT/swift-cache" \
  tools/FastProcessRunnerSmoke.swift ui/FastProcessRunner.swift \
  -o "$VERIFY_ROOT/FastProcessRunnerSmoke"
"$VERIFY_ROOT/FastProcessRunnerSmoke" "$VERIFY_ROOT/ezcloud"

echo "Local verification: PASS"
