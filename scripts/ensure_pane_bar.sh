#!/usr/bin/env bash
# Ensure pane-bar exists in the current window
# Creates a thin pane at the top running the pane-bar TUI
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Check if pane-bar binary exists
if [ ! -f "$CURRENT_DIR/bin/pane-bar" ]; then
    exit 0
fi

# Check if pane-bar is already running in this window
PANE_BAR_EXISTS=$(tmux list-panes -F '#{pane_current_command}' 2>/dev/null | grep -c '^pane-bar$' || true)
if [ "$PANE_BAR_EXISTS" -gt 0 ]; then
    exit 0
fi

# Check if there are any real panes (not sidebar/tabbar/pane-bar)
PANE_COUNT=$(tmux list-panes -F '#{pane_current_command}' 2>/dev/null | grep -cvE '^(sidebar|tabbar|pane-bar)$' || true)
if [ "$PANE_COUNT" -eq 0 ]; then
    exit 0
fi

# Get current pane ID before adding pane-bar
CURRENT_PANE=$(tmux display-message -p '#{pane_id}')

# Create pane-bar at the top of the window (1 line height)
tmux split-window -v -b -l 1 -c "#{pane_current_path}" "exec \"$CURRENT_DIR/bin/pane-bar\""

# Return focus to the original pane
tmux select-pane -t "$CURRENT_PANE"
