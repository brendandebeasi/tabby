#!/usr/bin/env bash
# Configure keyboard shortcuts following tmux conventions

# Direct window access with prefix + number (standard tmux)
tmux bind-key 1 select-window -t :=0
tmux bind-key 2 select-window -t :=1
tmux bind-key 3 select-window -t :=2
tmux bind-key 4 select-window -t :=3
tmux bind-key 5 select-window -t :=4
tmux bind-key 6 select-window -t :=5
tmux bind-key 7 select-window -t :=6
tmux bind-key 8 select-window -t :=7
tmux bind-key 9 select-window -t :=8
tmux bind-key 0 select-window -t :=9

echo "Keyboard shortcuts configured (standard tmux conventions):"
echo "  prefix + c - Create new window"
echo "  prefix + n - Next window"
echo "  prefix + p - Previous window"
echo "  prefix + 1-9,0 - Switch to window by number"
echo "  prefix + x - Kill current pane"
echo "  prefix + q - Display pane numbers"
echo "  prefix + w - Window list"
echo "  prefix + , - Rename window"
echo "  prefix + \" - Split horizontal"
echo "  prefix + % - Split vertical"
echo "  prefix + d - Detach from session"
echo ""
echo "Tabby-specific shortcuts:"
echo "  prefix + Tab - Toggle sidebar"
echo "  prefix + G - Create new group"
echo "  prefix + M - Toggle mode"
echo "  prefix + V - Switch to vertical mode"
echo "  prefix + H - Switch to horizontal mode"
