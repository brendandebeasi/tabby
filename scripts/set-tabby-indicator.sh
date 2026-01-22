#!/bin/bash
# set-tabby-indicator.sh - Set tabby indicators on the correct Claude window
# Usage: set-tabby-indicator.sh [busy|bell] [0|1]

INDICATOR="$1"
VALUE="$2"

# Find the window to set indicator on
# Uses the currently ACTIVE window since that's where the user is interacting with Claude
find_window() {
    tmux display-message -p '#{window_index}' 2>/dev/null
}

WIN_IDX=$(find_window)
if [ -z "$WIN_IDX" ]; then
    # Fallback: use current window
    WIN_IDX=$(tmux display-message -p '#{window_index}' 2>/dev/null)
fi

if [ -n "$WIN_IDX" ]; then
    case "$INDICATOR" in
        busy)
            if [ "$VALUE" = "1" ]; then
                tmux set-option -t ":$WIN_IDX" -w @tabby_busy 1 2>/dev/null
            else
                tmux set-option -t ":$WIN_IDX" -wu @tabby_busy 2>/dev/null
            fi
            ;;
        bell)
            if [ "$VALUE" = "1" ]; then
                tmux set-option -t ":$WIN_IDX" -w @tabby_bell 1 2>/dev/null
            else
                tmux set-option -t ":$WIN_IDX" -wu @tabby_bell 2>/dev/null
            fi
            ;;
    esac
fi

# Signal all sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
