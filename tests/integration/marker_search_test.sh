#!/usr/bin/env bash
set -e

echo "=== Integration Test: Marker Search Script ==="

SESSION_NAME="marker-test-$$"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
SET_MARKER_SCRIPT="$PROJECT_ROOT/scripts/set_window_marker.sh"

cleanup() {
    tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
}

trap cleanup EXIT

tmux new-session -d -s "${SESSION_NAME}" -n "marker-win"

bash "$SET_MARKER_SCRIPT" "0" "rocket" "$SESSION_NAME"
MARKER_VAL="$(tmux show-window-options -t "${SESSION_NAME}:0" -v @tabby_icon 2>/dev/null || true)"
if [ "$MARKER_VAL" != "🚀" ]; then
    echo "✗ Expected rocket marker, got: $MARKER_VAL"
    exit 1
fi
echo "✓ Keyword search sets mapped marker"

bash "$SET_MARKER_SCRIPT" "0" "🐱" "$SESSION_NAME"
MARKER_VAL="$(tmux show-window-options -t "${SESSION_NAME}:0" -v @tabby_icon 2>/dev/null || true)"
if [ "$MARKER_VAL" != "🐱" ]; then
    echo "✗ Expected direct marker, got: $MARKER_VAL"
    exit 1
fi
echo "✓ Direct marker input preserved"

echo "=== Marker search test passed ==="
