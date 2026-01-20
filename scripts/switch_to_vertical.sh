#!/usr/bin/env bash
# Switch to vertical sidebar mode

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Close any tabbar panes first
tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | \
    grep "^tabbar|" | \
    cut -d'|' -f2 | \
    while read -r pane_id; do
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done

# Disable tmux status bar (we'll use sidebar instead)
tmux set-option -g status off

# Mark sidebar as enabled
echo "enabled" > "$SIDEBAR_STATE_FILE"
tmux set-option @tmux-tabs-sidebar "enabled"

# Get current window to return to
CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

# Open sidebar in all windows that don't have one (silently)
tmux list-windows -F "#{window_id}" | while read -r window_id; do
    if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"; then
        tmux split-window -t "$window_id" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" 2>/dev/null || true
    fi
done

# Return to original window and focus main pane
tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
tmux select-pane -t "{right}" 2>/dev/null || true

tmux display-message "Vertical sidebar mode"
