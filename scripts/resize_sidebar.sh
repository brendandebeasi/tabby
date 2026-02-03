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

# Proportional pane resize: ensure non-sidebar panes maintain relative sizes
# Get all windows and re-apply their layouts to maintain proportions
tmux list-windows -F '#{window_id}' 2>/dev/null | while read -r window_id; do
    # Skip if window doesn't exist
    [ -z "$window_id" ] && continue

    # Get pane count excluding utility panes (sidebar, headers)
    pane_count=$(tmux list-panes -t "$window_id" -F '#{pane_current_command}' 2>/dev/null | \
        grep -cvE '(sidebar|sidebar-renderer|pane-header)' || echo 0)

    # Only process windows with multiple content panes
    if [ "$pane_count" -gt 1 ]; then
        # Try to restore saved layout for this window (preserves exact ratios)
        saved_layout=$(tmux show-window-option -t "$window_id" -v @tabby_saved_layout 2>/dev/null)
        if [ -n "$saved_layout" ]; then
            tmux select-layout -t "$window_id" "$saved_layout" 2>/dev/null || true
        fi
    fi
done

# Fix header pane heights (they can get compressed during resize)
# Headers should normally be 1 line. In custom-border mode they can expand to 2 when a drag handle is shown.
CUSTOM_BORDER=$(tmux show-option -gqv @tabby_custom_border 2>/dev/null)
HEADER_HEIGHT=1
if [ "$CUSTOM_BORDER" = "true" ] || [ "$CUSTOM_BORDER" = "1" ]; then
    HEADER_HEIGHT=2
fi

tmux list-panes -a -F '#{pane_id}:#{pane_height}:#{pane_start_command}' 2>/dev/null | while IFS=: read -r pane_id height start_cmd; do
    # Check if this is a pane-header
    if echo "$start_cmd" | grep -q "pane-header"; then
        # Resize to correct height when compressed (but don't force taller than expected max)
        if [ "$height" -lt "$HEADER_HEIGHT" ]; then
            tmux resize-pane -t "$pane_id" -y "$HEADER_HEIGHT" 2>/dev/null || true
        fi
    fi
done

# Signal daemon to refresh (pick up new dimensions)
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null)
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
if [ -f "$DAEMON_PID_FILE" ]; then
    kill -USR1 "$(cat "$DAEMON_PID_FILE")" 2>/dev/null || true
fi
