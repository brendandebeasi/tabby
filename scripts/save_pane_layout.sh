#!/usr/bin/env bash
# Save current window layout to a tmux option for later restoration.
# Called from after-select-pane, after-split-window, and kill-pane-wrapper.
#
# Usage:
#   save_pane_layout.sh [window_id] [window_layout]
# If args are provided, use them directly (avoids extra tmux calls).
# If no args, query tmux for current window.

WINDOW_ID="${1:-$(tmux display-message -p '#{window_id}' 2>/dev/null)}"
LAYOUT="${2:-$(tmux display-message -p '#{window_layout}' 2>/dev/null)}"

[ -z "$WINDOW_ID" ] || [ -z "$LAYOUT" ] && exit 0

# Store layout per-window using a global option keyed by window ID
tmux set-option -g "@tabby_layout_${WINDOW_ID}" "$LAYOUT" 2>/dev/null || true
exit 0
