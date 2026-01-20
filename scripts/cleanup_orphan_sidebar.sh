#!/usr/bin/env bash
# Close window if only sidebar/tabbar panes remain (main shell exited)
# Called by pane-exited hook

# Small delay to let tmux finish cleaning up the exited pane
sleep 0.1

# Get panes in current window
PANES=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null)

# Count non-sidebar/tabbar panes
MAIN_PANES=$(echo "$PANES" | grep -cvE "^(sidebar|tabbar)$" || true)

# If no main panes left, kill the window
if [ "$MAIN_PANES" -eq 0 ]; then
    tmux kill-window
fi
