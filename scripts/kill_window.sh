#!/usr/bin/env bash
# Kill a window and switch to last/first window, then refresh sidebars
# Usage: kill_window.sh <window_index>

WINDOW_INDEX="$1"

if [ -z "$WINDOW_INDEX" ]; then
    echo "Usage: kill_window.sh <window_index>" >&2
    exit 1
fi

# Kill the window
tmux kill-window -t ":$WINDOW_INDEX"

# Switch to last window or first available
tmux last-window 2>/dev/null || tmux select-window -t :0 2>/dev/null || true

# Focus the main pane (not the sidebar)
main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
if [ -n "$main_pane" ]; then
    tmux select-pane -t "$main_pane"
fi

# Brief delay then refresh all sidebars
sleep 0.1
for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null || true
done

exit 0
