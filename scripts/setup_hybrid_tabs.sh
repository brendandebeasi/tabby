#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

tmux set-option -g status on
tmux set-option -g status-position top
tmux set-option -g status-justify left
tmux set-option -g status-style "bg=default"

tmux set-option -g status-left ""
tmux set-option -g status-left-length 0

tmux set-option -g status-right "#[fg=#27ae60,bold][+] "
tmux set-option -g status-right-length 20

tmux set-window-option -g window-status-style "fg=#3498db,bg=default"
tmux set-window-option -g window-status-current-style "fg=colour38,bg=default,bold"

tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab normal #I '#W' '#{window_flags}')"
tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab active #I '#W' '#{window_flags}')"

tmux set-window-option -g window-status-separator ""

tmux set-option -g mouse on

tmux bind-key -T root MouseDown2Status kill-window
tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%'"

tmux bind-key -T root MouseDown1StatusRight new-window

echo "Hybrid clickable tabs configured!"
