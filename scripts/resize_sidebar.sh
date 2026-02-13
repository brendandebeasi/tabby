#!/usr/bin/env bash
# Resize sidebar panes and maintain proportional pane ratios after terminal resize.
#
# BUG FIX: Previously this script read the CURRENT sidebar pane width
# (which tmux may have compressed to 1-2 columns) and saved that as the
# desired width. Now it reads the SAVED desired width and enforces it.

MIN_WIDTH=15

# Read the saved desired width (set by grow/shrink buttons or initial setup)
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width 2>/dev/null)
if [ -z "$SIDEBAR_WIDTH" ] || [ "$SIDEBAR_WIDTH" -lt "$MIN_WIDTH" ] 2>/dev/null; then
    SIDEBAR_WIDTH=25
fi

# Apply saved width to all sidebar panes across all windows
tmux list-panes -a -F '#{pane_id}:#{pane_current_command}:#{pane_start_command}' 2>/dev/null | grep -E ':(sidebar|sidebar-renderer)' | while IFS=: read -r pane_id _; do
    tmux resize-pane -t "$pane_id" -x "$SIDEBAR_WIDTH" 2>/dev/null || true
done

# Signal daemon to refresh (pick up new dimensions)
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null)
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
if [ -f "$DAEMON_PID_FILE" ]; then
    kill -USR1 "$(cat "$DAEMON_PID_FILE")" 2>/dev/null || true
fi
