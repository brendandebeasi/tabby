#!/usr/bin/env bash
# Switch to vertical sidebar mode (daemon-based)

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Remove horizontal tabbar panes first.
while IFS= read -r line; do
    pane_id=$(echo "$line" | cut -d'|' -f2)
    tmux kill-pane -t "$pane_id" 2>/dev/null || true
done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^tabbar|" || true)

# Force vertical state if currently not enabled.
CURRENT_STATE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ "$CURRENT_STATE" != "enabled" ]; then
    exec "$CURRENT_DIR/scripts/toggle_sidebar.sh"
fi

# Already in vertical mode; ensure status bar stays hidden.
tmux set-option -g status off
