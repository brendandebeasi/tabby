#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

if [ ! -d "$ROOT_DIR/.git" ]; then
  echo "Not a git repository: $ROOT_DIR" >&2
  exit 1
fi

mkdir -p "$ROOT_DIR/.git/hooks"
ln -sf "../../.githooks/pre-commit" "$ROOT_DIR/.git/hooks/pre-commit"
chmod +x "$ROOT_DIR/.githooks/pre-commit" "$ROOT_DIR/scripts/check-commit-hygiene.sh"

echo "Installed pre-commit hook -> .git/hooks/pre-commit"
echo "Hook runs scripts/check-commit-hygiene.sh before each commit."
