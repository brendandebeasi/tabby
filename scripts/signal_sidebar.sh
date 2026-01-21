#!/usr/bin/env bash
# Signal sidebar to refresh window list
SESSION_ID=$(tmux display-message -p '#{session_id}')
PID_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.pid"

if [ -f "$PID_FILE" ]; then
    SIDEBAR_PID=$(cat "$PID_FILE")
    if [ -n "$SIDEBAR_PID" ] && kill -0 "$SIDEBAR_PID" 2>/dev/null; then
        # Send SIGUSR1 to trigger refresh
        kill -USR1 "$SIDEBAR_PID" 2>/dev/null || true
    fi
fi
