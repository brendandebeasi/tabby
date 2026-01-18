#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

tmux set-option -g status off

echo "open" > "/tmp/tmux-tabs-sidebar-global.state"

sidebar_exists=false
if tmux list-panes -F "#{pane_current_command}" | grep -q "sidebar"; then
    sidebar_exists=true
fi

if [ "$sidebar_exists" = "false" ]; then
    "$CURRENT_DIR/toggle_sidebar.sh"
fi

tmux display-message "Switched to vertical sidebar mode"
