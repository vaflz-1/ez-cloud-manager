#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kervik-verify.XXXXXX")"
trap 'rm -rf "$VERIFY_ROOT"' EXIT

export GOCACHE="$VERIFY_ROOT/go-cache"
export GOTMPDIR="$VERIFY_ROOT/go-tmp"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$VERIFY_ROOT/swift-cache"

cd "$PROJECT_DIR"

echo "[1/8] Go tests"
go test -count=1 ./...

echo "[2/8] Go race detector"
go test -race -count=1 ./...

echo "[3/8] Go vet"
go vet ./...

echo "[4/8] Swift macOS 13 typecheck"
swiftc -target "$(uname -m)-apple-macosx13.0" -warnings-as-errors \
  -file-prefix-map "$PROJECT_DIR=." -debug-prefix-map "$PROJECT_DIR=." \
  -module-cache-path "$VERIFY_ROOT/swift-cache" \
  -typecheck ui/*.swift -framework AppKit

echo "[5/8] Metadata and shell validation"
plutil -lint Info.plist EZCloudManager.entitlements
bash -n build.sh
git diff --check

echo "[6/8] Deterministic icon"
ICON_CHECK_ROOT="$VERIFY_ROOT/icon-check"
mkdir -p "$ICON_CHECK_ROOT/tools" "$ICON_CHECK_ROOT/assets"
cp tools/generate-icon.swift "$ICON_CHECK_ROOT/tools/generate-icon.swift"
cp assets/EZCloudManagerAppIcon.master.png \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.master.png"
swift -module-cache-path "$VERIFY_ROOT/swift-cache" \
  "$ICON_CHECK_ROOT/tools/generate-icon.swift"
cmp assets/EZCloudManagerAppIcon.icns \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.icns"
cmp assets/EZCloudManagerAppIcon.preview.png \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.preview.png"
diff -rq assets/EZCloudManagerAppIcon.iconset \
  "$ICON_CHECK_ROOT/assets/EZCloudManagerAppIcon.iconset"

echo "[7/8] Local secret scan"
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks dir . --redact --exit-code 1
  gitleaks git . --redact --exit-code 1
else
  echo "warning: gitleaks is not installed; secret scan skipped" >&2
fi

echo "[8/8] Process-boundary smoke"
go build -trimpath -o "$VERIFY_ROOT/ezcloud" ./cmd/ezcloud
swiftc -O -whole-module-optimization \
  -target "$(uname -m)-apple-macosx13.0" \
  -module-cache-path "$VERIFY_ROOT/swift-cache" \
  tools/FastProcessRunnerSmoke.swift ui/FastProcessRunner.swift \
  -o "$VERIFY_ROOT/FastProcessRunnerSmoke"
"$VERIFY_ROOT/FastProcessRunnerSmoke" "$VERIFY_ROOT/ezcloud"

echo "Local verification: PASS"
