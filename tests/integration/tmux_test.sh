#!/usr/bin/env bash
set -e

echo "=== Integration Test: Horizontal Rendering ==="

tmux new-session -d -s test

tmux rename-window -t test:0 "SD|app"
tmux new-window -t test -n "GP|tool"
tmux new-window -t test -n "notes"

tmux set-option -g @tmux_tabs_test 1

tmux run-shell /plugin/tmux-tabs.tmux

tmux run-shell -b "/plugin/bin/render-status > /tmp/render.txt"

sleep 1
OUTPUT=$(cat /tmp/render.txt)

if echo "$OUTPUT" | grep -Fq "SD|app"; then
	echo "✓ SD window found"
else
	echo "✗ SD window missing"
	exit 1
fi

if echo "$OUTPUT" | grep -Fq "GP|tool"; then
	echo "✓ GP window found"
else
	echo "✗ GP window missing"
	exit 1
fi

echo "=== All integration tests passed ==="

tmux kill-session -t test
