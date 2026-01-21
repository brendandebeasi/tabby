#!/usr/bin/env bash
# Resize sidebar panes to consistent width after terminal resize
# Syncs all sidebar widths to match the current window's sidebar

# Get the sidebar pane ID and width from the current window
CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')
SIDEBAR_WIDTH=""

# Find sidebar in current window and get its width
for pane_info in $(tmux list-panes -t "$CURRENT_WINDOW" -F '#{pane_id}:#{pane_current_command}:#{pane_width}' 2>/dev/null); do
    pane_id=$(echo "$pane_info" | cut -d: -f1)
    pane_cmd=$(echo "$pane_info" | cut -d: -f2)
    pane_width=$(echo "$pane_info" | cut -d: -f3)

    if [ "$pane_cmd" = "sidebar" ]; then
        SIDEBAR_WIDTH="$pane_width"
        break
    fi
done

# Default width if no sidebar found
if [ -z "$SIDEBAR_WIDTH" ]; then
    SIDEBAR_WIDTH=25
fi

# Store the width in a tmux option for persistence
tmux set-option -g @tabby_sidebar_width "$SIDEBAR_WIDTH" 2>/dev/null || true

# Apply to all sidebar panes across all windows
tmux list-panes -a -F '#{pane_id}:#{pane_current_command}' 2>/dev/null | grep ':sidebar$' | while IFS=: read -r pane_id _; do
    tmux resize-pane -t "$pane_id" -x "$SIDEBAR_WIDTH" 2>/dev/null || true
done

# Signal all sidebars to refresh (pick up new width)
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null || true
done
