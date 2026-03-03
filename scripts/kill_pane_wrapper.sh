#!/usr/bin/env bash
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WINDOW_INDEX=$(tmux display-message -p '#{window_index}' 2>/dev/null || echo "")
CONTENT_COUNT=$(tmux list-panes -F '#{pane_current_command}|#{pane_start_command}' 2>/dev/null | awk -F'|' '$1 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $2 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ {c++} END {print c+0}')

if [ -n "$WINDOW_INDEX" ] && [ "$CONTENT_COUNT" -le 1 ]; then
    "$CURRENT_DIR/scripts/kill_window.sh" "$WINDOW_INDEX"
    exit 0
fi

"$CURRENT_DIR/scripts/save_pane_layout.sh"
sleep 0.01
tmux kill-pane "$@"
