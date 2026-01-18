#!/usr/bin/env bash

echo "Restoring simple tabs..."

for i in 0 1 2 3 4 5 6 7 8 9 10; do
    tmux set-option -gu "status-format[$i]" 2>/dev/null || true
done

tmux set-option -g status on
tmux set-option -g status-position top
tmux set-option -g status-style "bg=default"

tmux set-option -g status-left ""
tmux set-option -g status-right ""

tmux set-window-option -g window-status-format "#I:#W#F"
tmux set-window-option -g window-status-current-format "#[bold]#I:#W#F"
tmux set-window-option -g window-status-separator " | "

tmux set-window-option -g window-status-style "fg=colour244"
tmux set-window-option -g window-status-current-style "fg=colour255,bold"

tmux refresh-client -S

echo "Restored to simple tmux tabs"
