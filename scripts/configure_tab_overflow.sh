#!/usr/bin/env bash

tmux set-option -g status-justify left

tmux set-window-option -g window-status-activity-style none
tmux set-window-option -g window-status-bell-style none

tmux set-option -g status-left-length 1
tmux set-option -g status-right-length 20

tmux set-option -gw automatic-rename off
tmux set-option -gw allow-rename on

echo "Tab overflow configured. Tmux will automatically handle overflow with ellipsis."
