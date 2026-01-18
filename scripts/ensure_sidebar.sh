#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
WINDOW_ID=$(tmux display-message -p '#{window_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

if [ -f "$SIDEBAR_STATE_FILE" ]; then
    SIDEBAR_ENABLED=$(grep -q "enabled" "$SIDEBAR_STATE_FILE" && echo "true" || echo "false")
    
    if [ "$SIDEBAR_ENABLED" = "true" ]; then
        SIDEBAR_COUNT=$(tmux list-panes -F "#{pane_current_command}" | grep -c "^sidebar$" || echo "0")
        
        if [ "$SIDEBAR_COUNT" -eq 0 ]; then
            tmux split-window -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\""
        fi
    fi
fi
