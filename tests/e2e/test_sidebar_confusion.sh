#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

echo "Testing sidebar window tracking..."
echo ""

echo "Opening sidebar..."
"$PROJECT_ROOT/scripts/toggle_sidebar.sh"
sleep 1

SIDEBAR_PANE=$(tmux list-panes -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar" | cut -d'|' -f2)

if [ -z "$SIDEBAR_PANE" ]; then
    echo "ERROR: Could not find sidebar pane"
    exit 1
fi

echo "Creating test windows..."
tmux new-window -n "sidebar-test-1" -t 5
tmux new-window -n "sidebar-test-2" -t 6
tmux new-window -n "sidebar-test-3" -t 7
sleep 1

echo "Initial sidebar content:"
tmux capture-pane -t "$SIDEBAR_PANE" -p | grep -E "^\s*(\[|>)" | head -20
echo ""

echo "Killing middle window (6)..."
tmux kill-window -t 6
sleep 1

echo "Sidebar after kill (should show windows 5 and 7 renumbered to 5 and 6):"
tmux capture-pane -t "$SIDEBAR_PANE" -p | grep -E "^\s*(\[|>)" | head -20
echo ""

echo "Click simulation test - selecting window via sidebar..."
tmux send-keys -t "$SIDEBAR_PANE" "jjj" 
sleep 0.5
tmux send-keys -t "$SIDEBAR_PANE" "Enter"
sleep 0.5

echo "Current window after sidebar selection:"
tmux display-message -p "Window: #{window_index} - #{window_name}"
echo ""

echo "Cleaning up..."
tmux kill-window -t "sidebar-test-1" 2>/dev/null || true
tmux kill-window -t "sidebar-test-3" 2>/dev/null || true
"$PROJECT_ROOT/scripts/toggle_sidebar.sh"
