#!/usr/bin/env bash
set -e

echo "=== Integration Test: Resize Stability Guard ==="

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)/scripts/signal_sidebar.sh"

if grep -q 'select-layout -t' "$SCRIPT"; then
    echo "✗ signal_sidebar.sh must not force select-layout (causes pane drift)"
    exit 1
fi

if grep -q 'resize-pane -t .* -y' "$SCRIPT"; then
    echo "✗ signal_sidebar.sh must not force pane heights (causes shrink loops)"
    exit 1
fi

if grep -q 'kill -USR1' "$SCRIPT"; then
    echo "✓ signal_sidebar.sh sends USR1 signal to daemon"
else
    echo "✗ signal_sidebar.sh should send USR1 signal"
    exit 1
fi

echo "=== Resize stability guard test passed ==="
