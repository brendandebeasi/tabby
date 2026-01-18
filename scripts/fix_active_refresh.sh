#!/usr/bin/env bash

tmux set-hook -g after-select-window "run-shell 'tmux refresh-client -S'"
tmux set-option -g status-interval 1

echo "Active window refresh fixed. Status will update when switching windows."
