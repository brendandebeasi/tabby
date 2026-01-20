#!/usr/bin/env bash
# Toggle tmux-tabs sidebar

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
SIDEBAR_WIDTH=25

# Renumber windows to ensure sequential indices (0, 1, 2, ...)
tmux move-window -r 2>/dev/null || true

# Check if any sidebar exists in session
SIDEBAR_EXISTS=$(tmux list-panes -s -F "#{pane_current_command}" 2>/dev/null | grep -c "^sidebar$" || echo "0")

if [ "$SIDEBAR_EXISTS" -gt 0 ]; then
    # Sidebar exists - close all sidebars in session
    while IFS= read -r line; do
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^sidebar|" || true)

    echo "disabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "disabled"
    tmux set-option -g status on
else
    # No sidebar - open in all windows
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "enabled"

    # Close any tabbar panes first
    while IFS= read -r line; do
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^tabbar|" || true)

    tmux set-option -g status off

    # Get current window before making changes
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

    # Open sidebar in all windows
    while IFS= read -r window_id; do
        [ -z "$window_id" ] && continue
        # Check if window already has sidebar
        if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"; then
            FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
            if [ -n "$FIRST_PANE" ]; then
                tmux split-window -t "$FIRST_PANE" -h -b -l "$SIDEBAR_WIDTH" "exec \"$CURRENT_DIR/bin/sidebar\"" || true
            fi
        fi
    done < <(tmux list-windows -F "#{window_id}")

    # Return to original window and focus main pane
    tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
    tmux select-pane -t "{right}" 2>/dev/null || true
fi
