#!/usr/bin/env bash
# Combined handler for pane selection - minimal for speed

SESSION_ID=$(tmux display-message -p '#{session_id}')

# Signal sidebar
PID_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    read -r PID < "$PID_FILE"
    kill -USR1 "$PID" 2>/dev/null || true
fi

# Signal pane bar
PID_FILE="/tmp/tmux-tabs-panebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    read -r PID < "$PID_FILE"
    kill -USR1 "$PID" 2>/dev/null || true
fi

exit 0
