#!/usr/bin/env bash
# Signal sidebar to refresh window list
SESSION_ID=$(tmux display-message -p '#{session_id}')
STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

if [ -f "$STATE_FILE" ]; then
    SIDEBAR_PANE=$(cat "$STATE_FILE")
    if [ -n "$SIDEBAR_PANE" ]; then
        # Send refresh signal to sidebar pane via tmux
        # The sidebar binary listens for SIGUSR1
        tmux send-keys -t "$SIDEBAR_PANE" "" 2>/dev/null || true
    fi
fi
