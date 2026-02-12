#!/usr/bin/env bash
set -e

echo "=== Integration Test: Resize Stability Guard ==="

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)/scripts/resize_sidebar.sh"

if grep -q 'select-layout -t' "$SCRIPT"; then
    echo "✗ resize_sidebar.sh must not force select-layout (causes pane drift)"
    exit 1
fi

if grep -q 'resize-pane -t .* -y' "$SCRIPT"; then
    echo "✗ resize_sidebar.sh must not force pane heights (causes shrink loops)"
    exit 1
fi

if grep -q 'resize-pane -t .* -x' "$SCRIPT"; then
    echo "✓ resize_sidebar.sh only enforces sidebar width"
else
    echo "✗ resize_sidebar.sh should enforce sidebar width"
    exit 1
fi

echo "=== Resize stability guard test passed ==="
