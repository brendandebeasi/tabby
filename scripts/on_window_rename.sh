#!/usr/bin/env bash
# Handler for window rename - lock the name and refresh sidebar

# Get the window that was just renamed
WINDOW_ID=$(tmux display-message -p '#{window_id}')

# Lock the window name so tabby's syncWindowNames won't overwrite it
tmux set-window-option -t "$WINDOW_ID" @tabby_name_locked 1 2>/dev/null || true

# Signal sidebar to refresh
SESSION_ID=$(tmux display-message -p '#{session_id}')
PID_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    read -r PID < "$PID_FILE"
    kill -USR1 "$PID" 2>/dev/null || true
fi

# Refresh status bar
tmux refresh-client -S 2>/dev/null || true

exit 0
