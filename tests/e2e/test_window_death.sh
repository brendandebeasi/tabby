#!/usr/bin/env bash
set -euo pipefail

echo "Testing for window death issues..."

initial_windows=$(tmux list-windows -F "#{window_id} #{window_index} #{window_name}")
echo "Initial windows:"
echo "$initial_windows"
echo ""

echo "Test 1: Creating new window..."
tmux new-window -n "test-death"
sleep 0.5

echo "Test 2: Switching to new window..."
tmux select-window -t "test-death"
sleep 0.5

echo "Test 3: Toggling sidebar..."
$PROJECT_ROOT/scripts/toggle_sidebar.sh
sleep 1

echo "Test 4: Switching back to original window..."
tmux select-window -t 0
sleep 0.5

echo "Test 5: Checking if test window still exists..."
if tmux list-windows | grep -q "test-death"; then
    echo "✓ Window survived sidebar toggle"
else
    echo "✗ Window died during sidebar toggle!"
fi

echo ""
echo "Final windows:"
tmux list-windows -F "#{window_id} #{window_index} #{window_name}"

echo ""
echo "Test 6: Cleaning up test window..."
tmux kill-window -t "test-death" 2>/dev/null || true
