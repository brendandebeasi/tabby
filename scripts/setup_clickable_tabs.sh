#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

tmux set-option -g mouse on

tmux set-window-option -g window-status-format "#[fg=colour245]#I:#W#F"
tmux set-window-option -g window-status-current-format "#[fg=colour250,bold]#I:#W#F"
tmux set-window-option -g window-status-separator " | "

tmux set-option -g status-justify left
tmux set-option -g status-left ""
tmux set-option -g status-right "#[fg=#27ae60][+] "

tmux bind-key -T root MouseUp2Status kill-window
tmux bind-key -T root MouseUp3Status command-prompt -I "#W" "rename-window '%%'"

echo "Clickable tabs configured. Mouse support enabled."
echo "- Left click: Switch to window"
echo "- Middle click: Close window"
echo "- Right click: Rename window"
echo "- prefix + c: Create new window"
