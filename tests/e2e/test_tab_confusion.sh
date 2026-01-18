#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="/Users/b/git/tmux-tabs"

echo "Testing for tab confusion issues..."
echo ""

echo "Creating test scenario..."
tmux new-window -n "confusion-test-A" -t 2
tmux new-window -n "confusion-test-B" -t 3
tmux new-window -n "confusion-test-C" -t 4
sleep 0.5

echo "Initial state:"
tmux list-windows -F "#{window_index}: #{window_name} (ID: #{window_id})"
echo ""

echo "Render-status output:"
"$PROJECT_ROOT/bin/render-status" | sed 's/#\[[^]]*\]//g' | sed 's/[[:space:]]\+/ /g'
echo ""

echo "Test 1: Kill middle window..."
tmux kill-window -t 3
sleep 0.5

echo "After killing window 3:"
tmux list-windows -F "#{window_index}: #{window_name} (ID: #{window_id})"
echo ""

echo "Render-status after kill:"
"$PROJECT_ROOT/bin/render-status" | sed 's/#\[[^]]*\]//g' | sed 's/[[:space:]]\+/ /g'
echo ""

echo "Test 2: Create new window (should take index 3)..."
tmux new-window -n "confusion-test-D"
sleep 0.5

echo "After creating new window:"
tmux list-windows -F "#{window_index}: #{window_name} (ID: #{window_id})"
echo ""

echo "Render-status after new window:"
"$PROJECT_ROOT/bin/render-status" | sed 's/#\[[^]]*\]//g' | sed 's/[[:space:]]\+/ /g'
echo ""

echo "Test 3: Rename windows to check mapping..."
tmux rename-window -t 2 "renamed-A"
tmux rename-window -t 4 "renamed-C"
sleep 0.5

echo "After renaming:"
tmux list-windows -F "#{window_index}: #{window_name} (ID: #{window_id})"
echo ""

echo "Render-status after rename:"
"$PROJECT_ROOT/bin/render-status" | sed 's/#\[[^]]*\]//g' | sed 's/[[:space:]]\+/ /g'
echo ""

echo "Cleaning up..."
tmux kill-window -t "renamed-A" 2>/dev/null || true
tmux kill-window -t "confusion-test-D" 2>/dev/null || true
tmux kill-window -t "renamed-C" 2>/dev/null || true
