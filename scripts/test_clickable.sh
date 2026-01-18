#!/usr/bin/env bash

echo "Testing clickable tabs setup..."

tmux set-option -g mouse on

tmux set-window-option -g window-status-format "#[fg=white]#I:#W"
tmux set-window-option -g window-status-current-format "#[fg=green,bold]#I:#W"

tmux bind-key -T root MouseDown1Status select-window -t =
tmux bind-key -T root MouseDown2Status kill-window
tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%'"

tmux refresh-client -S

echo "Basic clickable tabs configured. Try clicking on window numbers/names in the status bar."
echo "Left click = switch, Middle click = close, Right click = rename"
