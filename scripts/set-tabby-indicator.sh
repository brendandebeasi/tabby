#!/bin/bash
# set-tabby-indicator.sh - Set tabby indicators on Claude's window
# Usage: set-tabby-indicator.sh [busy|bell] [0|1]
#
# When busy=1 is set, it saves the active window index.
# When busy=0 or bell=1 is set, it uses the saved window.
# State is stored per-window to support multiple Claude sessions.

INDICATOR="$1"
VALUE="$2"

# Get the active window index
get_active_window() {
    tmux display-message -p '#{window_index}' 2>/dev/null
}

# State file: one per tmux session, stores window index
# Format: simple text file with just the window index
STATE_DIR="/tmp/tabby-state"
mkdir -p "$STATE_DIR" 2>/dev/null

# Get tmux session name for unique state file
SESSION=$(tmux display-message -p '#{session_name}' 2>/dev/null)
STATE_FILE="$STATE_DIR/claude-window-${SESSION}"

case "$INDICATOR" in
    busy)
        if [ "$VALUE" = "1" ]; then
            # Starting work - save the active window
            WIN_IDX=$(get_active_window)
            if [ -n "$WIN_IDX" ]; then
                echo "$WIN_IDX" > "$STATE_FILE"
                tmux set-option -t ":$WIN_IDX" -w @tabby_busy 1 2>/dev/null
            fi
        else
            # Finishing work - use saved window
            if [ -f "$STATE_FILE" ]; then
                WIN_IDX=$(cat "$STATE_FILE")
                tmux set-option -t ":$WIN_IDX" -wu @tabby_busy 2>/dev/null
            fi
        fi
        ;;
    bell)
        if [ "$VALUE" = "1" ]; then
            # Set bell on saved window
            if [ -f "$STATE_FILE" ]; then
                WIN_IDX=$(cat "$STATE_FILE")
                tmux set-option -t ":$WIN_IDX" -w @tabby_bell 1 2>/dev/null
                rm -f "$STATE_FILE"  # Clean up
            fi
        else
            # Clear bell on saved or active window
            if [ -f "$STATE_FILE" ]; then
                WIN_IDX=$(cat "$STATE_FILE")
            else
                WIN_IDX=$(get_active_window)
            fi
            [ -n "$WIN_IDX" ] && tmux set-option -t ":$WIN_IDX" -wu @tabby_bell 2>/dev/null
        fi
        ;;
esac

# Signal all sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
