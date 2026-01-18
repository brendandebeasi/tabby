#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

tmux list-panes -a -F "#{pane_id} #{pane_current_command}" | grep "sidebar" | while read -r pane_id cmd; do
    if [[ "$cmd" == *"sidebar"* ]]; then
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    fi
done

echo "closed" > "/tmp/tmux-tabs-sidebar-global.state"

tmux set-option -g status on
tmux set-option -g status-position top

for i in {0..10}; do
    tmux set-option -gu status-format[$i] 2>/dev/null || true
done

tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab normal #I '#W' '#{window_flags}')"
tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab active #I '#W' '#{window_flags}')"

tmux refresh-client -S

tmux display-message "Switched to horizontal tabs mode"
