#!/usr/bin/env bash

tmux bind-key -n M-h previous-window
tmux bind-key -n M-l next-window

tmux bind-key -n M-1 select-window -t :=0
tmux bind-key -n M-2 select-window -t :=1
tmux bind-key -n M-3 select-window -t :=2
tmux bind-key -n M-4 select-window -t :=3
tmux bind-key -n M-5 select-window -t :=4
tmux bind-key -n M-6 select-window -t :=5
tmux bind-key -n M-7 select-window -t :=6
tmux bind-key -n M-8 select-window -t :=7
tmux bind-key -n M-9 select-window -t :=8
tmux bind-key -n M-0 select-window -t :=9

tmux bind-key -n M-n new-window
tmux bind-key -n M-x kill-pane

tmux bind-key -n M-q display-panes

echo "Keyboard shortcuts configured:"
echo "  Alt+h - Previous window"
echo "  Alt+l - Next window"
echo "  Alt+1-9,0 - Switch to window by number"
echo "  Alt+n - Create new window"
echo "  Alt+x - Kill current pane"
echo "  Alt+q - Display pane numbers"
