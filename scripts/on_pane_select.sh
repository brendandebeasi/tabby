#!/usr/bin/env bash
# Combined handler for pane selection - minimal for speed

# optimization: Accept session ID as arg to avoid tmux call overhead
SESSION_ID="$1"

if [ -z "$SESSION_ID" ]; then
    # Fallback for manual calls
    SESSION_ID=$(tmux display-message -p '#{session_id}')
fi

# Signal daemon to refresh immediately
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
if [ -f "$DAEMON_PID_FILE" ]; then
    read -r PID < "$DAEMON_PID_FILE"
    kill -USR1 "$PID" 2>/dev/null || true
fi

# Signal pane bar (horizontal mode)
PID_FILE="/tmp/tmux-tabs-panebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    read -r PID < "$PID_FILE"
    kill -USR1 "$PID" 2>/dev/null || true
fi

exit 0