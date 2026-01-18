#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Force-enabling clickable tabs..."

for i in {0..10}; do
    tmux set-option -gu status-format[$i] 2>/dev/null || true
done

tmux set-option -gu status-format 2>/dev/null || true
tmux set-option -g status on
tmux set-option -g status-position top
tmux set-option -g status-justify left
tmux set-option -g status-style "bg=default"
tmux set-option -g status-interval 1

tmux set-option -g status-left ""
tmux set-option -g status-left-length 0
tmux set-option -g status-right "#[fg=#27ae60,bold][+] "
tmux set-option -g status-right-length 10

tmux set-window-option -g window-status-format "#I:#W"
tmux set-window-option -g window-status-current-format "#[bold]#I:#W"
tmux set-window-option -g window-status-separator " | "

tmux set-option -g mouse on

tmux unbind-key -T root MouseDown1Status 2>/dev/null || true
tmux unbind-key -T root MouseDown2Status 2>/dev/null || true
tmux unbind-key -T root MouseDown3Status 2>/dev/null || true
tmux unbind-key -T root MouseDown1StatusRight 2>/dev/null || true

tmux bind-key -T root MouseDown1Status select-window -t =
tmux bind-key -T root MouseDown2Status kill-window
tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%'"
tmux bind-key -T root MouseDown1StatusRight new-window

tmux refresh-client -S

echo "Basic clickable tabs enabled. Testing with simple format."
echo "Try clicking on '0:window-name' style tabs."
echo ""
echo "If clicking works, run: $CURRENT_DIR/apply_styled_clickable.sh"
