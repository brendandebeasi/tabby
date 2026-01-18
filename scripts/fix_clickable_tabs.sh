#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Fixing tmux-tabs clickable functionality..."

tmux set-option -g mouse on
tmux set-option -g status on
tmux set-option -g status-position top
tmux set-option -g status-interval 1
tmux set-option -g status-style "bg=default"

tmux set-option -g status-left ""
tmux set-option -g status-left-length 0

tmux set-option -g status-right "#[fg=#27ae60,bold][+] "
tmux set-option -g status-right-length 10

tmux set-window-option -g window-status-style "fg=#3498db,bg=default"
tmux set-window-option -g window-status-current-style "fg=colour38,bg=default,bold"

tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab normal #I '#W' '#{window_flags}')"
tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab active #I '#W' '#{window_flags}')"

tmux set-window-option -g window-status-separator ""

tmux unbind-key -T root MouseDown1Status
tmux bind-key -T root MouseDown1Status select-window -t =

tmux unbind-key -T root MouseDown2Status  
tmux bind-key -T root MouseDown2Status kill-window

tmux unbind-key -T root MouseDown3Status
tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%'"

tmux unbind-key -T root MouseDown1StatusRight
tmux bind-key -T root MouseDown1StatusRight new-window

tmux refresh-client -S

echo "✓ Mouse enabled"
echo "✓ Status bar configured" 
echo "✓ Window formats set"
echo "✓ Mouse bindings configured"
echo ""
echo "Tabs should now be clickable:"
echo "  • Left click on tab = switch to window"
echo "  • Middle click on tab = close window"
echo "  • Right click on tab = rename window"
echo "  • Click green [+] = new window"
echo ""
echo "Toggle modes with: prefix + M"
