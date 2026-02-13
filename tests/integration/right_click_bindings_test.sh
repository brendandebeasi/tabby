#!/usr/bin/env bash
set -e

echo "=== Integration Test: Right-click Context Routing ==="

SESSION_NAME="rightclick-bindings-$$"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
PLUGIN_TMUX="$PROJECT_ROOT/tabby.tmux"

cleanup() {
    tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
}
trap cleanup EXIT

tmux new-session -d -s "${SESSION_NAME}" -n "main"
tmux set-option -g @tabby_test 1
tmux run-shell -t "${SESSION_NAME}" -b "$PLUGIN_TMUX"
sleep 1

RIGHT_CLICK_BINDING="$(tmux list-keys -T root 2>/dev/null | grep 'MouseDown3Pane' | head -n 1 || true)"
if echo "$RIGHT_CLICK_BINDING" | grep -q 'send-keys -M -t ='; then
    echo "✓ MouseDown3Pane routes to clicked pane"
else
    echo "✗ MouseDown3Pane missing clicked-pane target"
    echo "  Binding: $RIGHT_CLICK_BINDING"
    exit 1
fi

LEFT_CLICK_BINDING="$(tmux list-keys -T root 2>/dev/null | grep 'MouseDown1Pane' | head -n 1 || true)"
if echo "$LEFT_CLICK_BINDING" | grep -q 'send-keys -M -t ='; then
    echo "✓ MouseDown1Pane pass-through routes to clicked pane"
else
    echo "✗ MouseDown1Pane missing clicked-pane target in pass-through path"
    echo "  Binding: $LEFT_CLICK_BINDING"
    exit 1
fi

DRAG_BINDING="$(tmux list-keys -T root 2>/dev/null | grep 'MouseDrag1Pane' | head -n 1 || true)"
if echo "$DRAG_BINDING" | grep -q 'send-keys -M -t ='; then
    echo "✓ MouseDrag1Pane routes drag to clicked pane"
else
    echo "✗ MouseDrag1Pane missing clicked-pane target"
    echo "  Binding: $DRAG_BINDING"
    exit 1
fi

echo "=== Right-click context routing test passed ==="
