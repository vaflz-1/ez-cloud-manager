#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOK_PATH="$PROJECT_DIR/.git/hooks/pre-push"

if [ ! -d "$PROJECT_DIR/.git" ]; then
  echo "error: $PROJECT_DIR is not a Git worktree" >&2
  exit 1
fi

if [ -e "$HOOK_PATH" ]; then
  echo "error: $HOOK_PATH already exists; refusing to overwrite it" >&2
  exit 1
fi

cat >"$HOOK_PATH" <<'HOOK'
#!/usr/bin/env bash
set -euo pipefail
PROJECT_DIR="$(git rev-parse --show-toplevel)"
exec "$PROJECT_DIR/tools/verify-local.sh"
HOOK
chmod 0700 "$HOOK_PATH"

echo "Installed local-only pre-push hook: $HOOK_PATH"
