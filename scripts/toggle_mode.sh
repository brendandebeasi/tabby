#!/usr/bin/env bash
# Toggle between horizontal (status bar) and vertical (sidebar) modes

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_FILE="/tmp/tmux-tabs-mode.state"
SESSION_ID=$(tmux display-message -p '#{session_id}')

current_mode=$(cat "$STATE_FILE" 2>/dev/null || echo "horizontal")

if [ "$current_mode" = "horizontal" ]; then
    # Switch to vertical (sidebar) mode
    "$CURRENT_DIR/switch_to_vertical.sh"
    echo "vertical" > "$STATE_FILE"
else
    # Switch to horizontal (status bar) mode
    "$CURRENT_DIR/switch_to_horizontal.sh"
    echo "horizontal" > "$STATE_FILE"
fi
