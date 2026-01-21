#!/usr/bin/env bash
# Signal pane-bar to refresh its display
# Sends SIGUSR1 to any pane-bar processes in the current window
set -eu

# Find pane-bar panes and send them a signal via tmux
tmux list-panes -F '#{pane_pid}:#{pane_current_command}' 2>/dev/null | while IFS=: read -r pid cmd; do
    if [ "$cmd" = "pane-bar" ]; then
        kill -USR1 "$pid" 2>/dev/null || true
    fi
done
