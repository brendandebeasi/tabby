#!/usr/bin/env bash
# Toggle tmux-tabs sidebar

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
SIDEBAR_WIDTH=25

# Renumber windows to ensure sequential indices (0, 1, 2, ...)
tmux move-window -r 2>/dev/null || true

# Get current state from tmux option (most reliable) or state file
CURRENT_STATE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$CURRENT_STATE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    CURRENT_STATE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

# If state is "enabled", we should close sidebars
# If state is anything else (disabled, horizontal, empty), we should open sidebars
if [ "$CURRENT_STATE" = "enabled" ]; then
    # Close all sidebars in session
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^sidebar|" || true)

    echo "disabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "disabled"
    tmux set-option -g status on
else
    # Open sidebars
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "enabled"

    # Close any tabbar panes first
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^tabbar|" || true)

    tmux set-option -g status off

    # Get current window before making changes
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

    # Open sidebar in all windows that don't have one
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

# Force refresh to ensure display is updated
tmux refresh-client -S 2>/dev/null || true
