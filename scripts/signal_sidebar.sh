#!/usr/bin/env bash
# Signal all sidebar/tabbar processes to refresh window list
# The sidebar and tabbar binaries listen for SIGUSR1

SESSION_ID=$(tmux display-message -p '#{session_id}')
STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Get current mode
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ] && [ -f "$STATE_FILE" ]; then
    MODE=$(cat "$STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
    # Signal sidebar panes
    for pid in $(tmux list-panes -s -F "#{pane_current_command}|#{pane_pid}" 2>/dev/null | grep "^sidebar|" | cut -d'|' -f2); do
        kill -USR1 "$pid" 2>/dev/null || true
    done
elif [ "$MODE" = "horizontal" ]; then
    # Signal tabbar panes
    for pid in $(tmux list-panes -s -F "#{pane_current_command}|#{pane_pid}" 2>/dev/null | grep "^tabbar|" | cut -d'|' -f2); do
        kill -USR1 "$pid" 2>/dev/null || true
    done
fi

exit 0
