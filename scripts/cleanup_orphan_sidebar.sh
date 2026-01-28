#!/usr/bin/env bash
# Close window if only sidebar/tabbar panes remain (main shell exited)
# Called by pane-exited hook

# Small delay to let tmux finish cleaning up the exited pane
sleep 0.05

# Get panes in current window (current command and start command)
PANES=$(tmux list-panes -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null)

# Count non-utility panes (sidebar, tabbar, pane-bar are utilities)
# Check for sidebar-renderer as well
MAIN_PANES=$(echo "$PANES" | grep -cvE "(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)" || true)

# If no main panes left, kill the window
if [ "$MAIN_PANES" -eq 0 ]; then
    tmux kill-window
fi

# Signal daemon for immediate refresh (update tab list, cleanup headers)
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null)
if [ -n "$SESSION_ID" ]; then
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        read -r PID < "$DAEMON_PID_FILE"
        kill -USR1 "$PID" 2>/dev/null || true
    fi
fi
