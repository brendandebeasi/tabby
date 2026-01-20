#!/usr/bin/env bash
# Switch to vertical sidebar mode

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Get current window to return to (before any pane changes)
CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

# Renumber windows to ensure sequential indices (0, 1, 2, ...)
tmux move-window -r 2>/dev/null || true

# Kill any tabbar panes first
while IFS= read -r line; do
    pane_id=$(echo "$line" | cut -d'|' -f2)
    tmux kill-pane -t "$pane_id" 2>/dev/null || true
done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^tabbar|" || true)

# Disable tmux status bar (we'll use sidebar instead)
tmux set-option -g status off

# Mark sidebar as enabled
echo "enabled" > "$SIDEBAR_STATE_FILE"
tmux set-option @tmux-tabs-sidebar "enabled"

# Open sidebar in all windows that don't have one
while IFS= read -r window_id; do
    [ -z "$window_id" ] && continue
    # Check if this window already has a sidebar pane
    if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"; then
        # Get the first pane in the window to split from
        FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -n "$FIRST_PANE" ]; then
            tmux split-window -t "$FIRST_PANE" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" || true
        fi
    fi
done < <(tmux list-windows -F "#{window_id}")

# Return to original window and focus main pane
tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
tmux select-pane -t "{right}" 2>/dev/null || true

