#!/usr/bin/env bash
# Switch to vertical sidebar mode (daemon-based)

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Force vertical state if currently not enabled.
CURRENT_STATE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")
if [ "$CURRENT_STATE" != "enabled" ]; then
    exec "$CURRENT_DIR/scripts/toggle_sidebar.sh"
fi

# Already in vertical mode; ensure status bar stays hidden.
tmux set-option -g status off
