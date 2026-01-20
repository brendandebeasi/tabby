#!/usr/bin/env bash
# Ensure sidebar/tabbar exists in current window when that mode is enabled
# Called by tmux hooks when windows are created/switched

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
SIDEBAR_WIDTH=25
TABBAR_HEIGHT=2

# Get mode from tmux option or state file
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
    # Check if CURRENT window has a sidebar
    SIDEBAR_COUNT=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -c "^sidebar$" || echo "0")

    if [ "$SIDEBAR_COUNT" -eq 0 ]; then
        # No sidebar in current window - add one
        FIRST_PANE=$(tmux list-panes -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -n "$FIRST_PANE" ]; then
            tmux split-window -t "$FIRST_PANE" -h -b -l "$SIDEBAR_WIDTH" "exec \"$CURRENT_DIR/bin/sidebar\"" || true
            tmux select-pane -t "{right}" 2>/dev/null || true
        fi
    fi
elif [ "$MODE" = "horizontal" ]; then
    # Check if CURRENT window has a tabbar
    TABBAR_COUNT=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -c "^tabbar$" || echo "0")

    if [ "$TABBAR_COUNT" -eq 0 ]; then
        FIRST_PANE=$(tmux list-panes -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -n "$FIRST_PANE" ]; then
            tmux split-window -t "$FIRST_PANE" -v -b -l "$TABBAR_HEIGHT" "exec \"$CURRENT_DIR/bin/tabbar\"" || true
            tmux select-pane -t "{bottom}" 2>/dev/null || true
        fi
    fi
fi
