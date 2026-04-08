#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

if [ ! -d "$ROOT_DIR/.git" ]; then
  echo "Not a git repository: $ROOT_DIR" >&2
  exit 1
fi

mkdir -p "$ROOT_DIR/.git/hooks"
for hook in pre-commit commit-msg prepare-commit-msg post-commit pre-push; do
  ln -sf "../../.githooks/$hook" "$ROOT_DIR/.git/hooks/$hook"
done

chmod +x \
  "$ROOT_DIR/.githooks/pre-commit" \
  "$ROOT_DIR/.githooks/commit-msg" \
  "$ROOT_DIR/.githooks/prepare-commit-msg" \
  "$ROOT_DIR/.githooks/post-commit" \
  "$ROOT_DIR/.githooks/pre-push" \
  "$ROOT_DIR/scripts/check-commit-hygiene.sh"

echo "Installed tracked git hooks in .git/hooks:"
echo "  - pre-commit -> scripts/check-commit-hygiene.sh"
echo "  - commit-msg, prepare-commit-msg, post-commit, pre-push -> repo-local no-ops"
echo "This also replaces any stale external hook manager hooks (for example, old Entire-managed hooks)."
