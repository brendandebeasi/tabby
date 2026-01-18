#!/usr/bin/env bash

current_mode="horizontal"
if tmux list-panes -F "#{pane_current_command}" | grep -q "sidebar"; then
    current_mode="vertical"
fi

tmux display-message -p "Current mode: $current_mode | Switch: Ctrl+b H (horizontal) | Ctrl+b V (vertical) | Ctrl+b M (toggle)"
