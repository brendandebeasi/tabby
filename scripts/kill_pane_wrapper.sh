#!/usr/bin/env bash
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Parse the target pane from arguments (expects "-t %ID")
TARGET_PANE=""
while [ $# -gt 0 ]; do
    case "$1" in
        -t) TARGET_PANE="$2"; shift 2 ;;
        *)  shift ;;
    esac
done

if [ -z "$TARGET_PANE" ]; then
    # Fallback: no target specified, use current pane
    TARGET_PANE=$(tmux display-message -p '#{pane_id}' 2>/dev/null)
fi

# Resolve window index from the target pane (not the active window)
WINDOW_INDEX=$(tmux display-message -t "$TARGET_PANE" -p '#{window_index}' 2>/dev/null || echo "")

# Count content panes in the target pane's window (not the active window)
CONTENT_COUNT=$(tmux list-panes -t "$TARGET_PANE" -F '#{pane_current_command}|#{pane_start_command}' 2>/dev/null | awk -F'|' '$1 !~ /(sidebar|renderer|pane-header|tabby-daemon)/ && $2 !~ /(sidebar|renderer|pane-header|tabby-daemon)/ {c++} END {print c+0}')

if [ -n "$WINDOW_INDEX" ] && [ "$CONTENT_COUNT" -le 1 ]; then
    "$CURRENT_DIR/scripts/kill_window.sh" "$WINDOW_INDEX"
    exit 0
fi

"$CURRENT_DIR/scripts/save_pane_layout.sh"
sleep 0.01
tmux kill-pane -t "$TARGET_PANE"
