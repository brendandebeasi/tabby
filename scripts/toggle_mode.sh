#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$HOME/.tmux/plugins/tmux-tabs/config.yaml"
STATE_FILE="/tmp/tmux-tabs-mode.state"

current_mode=$(cat "$STATE_FILE" 2>/dev/null || echo "horizontal")

if [ "$current_mode" = "horizontal" ]; then
    new_mode="vertical"
    
    tmux list-panes -a -F "#{pane_id} #{pane_current_command}" | grep "sidebar" | while read -r pane_id cmd; do
        if [[ "$cmd" == *"sidebar"* ]]; then
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
        fi
    done
    
    echo "open" > "/tmp/tmux-tabs-sidebar-global.state"
    
    current_window=$(tmux display-message -p '#I')
    for window in $(tmux list-windows -F '#I'); do
        tmux select-window -t "$window"
        "$CURRENT_DIR/toggle_sidebar.sh"
    done
    tmux select-window -t "$current_window"
    
    tmux set-option -g status off
else
    new_mode="horizontal"
    
    tmux list-panes -a -F "#{pane_id} #{pane_current_command}" | grep "sidebar" | while read -r pane_id cmd; do
        if [[ "$cmd" == *"sidebar"* ]]; then
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
        fi
    done
    
    tmux set-option -g status on
    tmux set-option -g status-position top
    
    tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab normal #I '#W' '#{window_flags}')"
    tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab active #I '#W' '#{window_flags}')"
    tmux set-window-option -g window-status-separator ""
    
    tmux set-option -g status-left ""
    tmux set-option -g status-right "#[fg=#27ae60,bold][+] "
    
    tmux set-option -g mouse on
fi

echo "$new_mode" > "$STATE_FILE"
tmux display-message "Switched to $new_mode tabs"
