#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: Git Hook Installation ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
TEST_ROOT="$(mktemp -d /tmp/tabby-githooks-test.XXXXXX)"
TEST_REPO="$TEST_ROOT/repo"

cleanup() {
  rm -rf "$TEST_ROOT" 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$TEST_REPO/.githooks" "$TEST_REPO/scripts" "$TEST_REPO/tests/integration"
cp "$PROJECT_ROOT/scripts/install-git-hooks.sh" "$TEST_REPO/scripts/install-git-hooks.sh"
cp "$PROJECT_ROOT/scripts/check-commit-hygiene.sh" "$TEST_REPO/scripts/check-commit-hygiene.sh"
cp "$PROJECT_ROOT/.githooks/"* "$TEST_REPO/.githooks/"

git -C "$TEST_REPO" init >/dev/null 2>&1

cat > "$TEST_REPO/.git/hooks/commit-msg" <<'EOF'
#!/bin/sh
entire hooks git commit-msg "$1"
EOF
chmod +x "$TEST_REPO/.git/hooks/commit-msg"

cat > "$TEST_REPO/.git/hooks/pre-push" <<'EOF'
#!/bin/sh
entire hooks git pre-push "$1"
EOF
chmod +x "$TEST_REPO/.git/hooks/pre-push"

"$TEST_REPO/scripts/install-git-hooks.sh" >/dev/null

assert_symlink() {
  local hook="$1"
  local expected="../../.githooks/$hook"
  local actual
  actual="$(readlink "$TEST_REPO/.git/hooks/$hook")"
  if [ "$actual" = "$expected" ]; then
    echo "✓ $hook installed as symlink"
  else
    echo "✗ $hook symlink target was '$actual', expected '$expected'" >&2
    exit 1
  fi
}

for hook in pre-commit commit-msg prepare-commit-msg post-commit pre-push; do
  assert_symlink "$hook"
  if grep -q "entire" "$TEST_REPO/.githooks/$hook"; then
    echo "✗ $hook still references entire" >&2
    exit 1
  fi
done

tmpmsg="$TEST_ROOT/COMMIT_EDITMSG"
printf 'test message\n' > "$tmpmsg"

"$TEST_REPO/.git/hooks/commit-msg" "$tmpmsg"
"$TEST_REPO/.git/hooks/prepare-commit-msg" "$tmpmsg" message
"$TEST_REPO/.git/hooks/post-commit"
"$TEST_REPO/.git/hooks/pre-push" origin

echo "✓ installed hooks run without external dependencies"
echo "=== Git hook installation test passed ==="
