#!/usr/bin/env bash
# Restore saved pane layout after a pane exits to preserve size ratios.
# Called from pane-exited hook. Without this, tmux equalizes pane sizes
# after a pane closes, which disrupts the user's layout.

WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null)
[ -z "$WINDOW_ID" ] && exit 0

# Check if we have a saved layout for this window
SAVED_LAYOUT=$(tmux show-option -gqv "@tabby_layout_${WINDOW_ID}" 2>/dev/null)
[ -z "$SAVED_LAYOUT" ] && exit 0

# Only attempt restore if more than one pane remains
PANE_COUNT=$(tmux list-panes -t "$WINDOW_ID" 2>/dev/null | wc -l | tr -d ' ')
[ "$PANE_COUNT" -le 1 ] && exit 0

# Apply the saved layout (may fail if pane count changed, that's fine)
tmux select-layout -t "$WINDOW_ID" "$SAVED_LAYOUT" 2>/dev/null || true

# Clear the saved layout since it's now stale (pane count changed)
tmux set-option -gu "@tabby_layout_${WINDOW_ID}" 2>/dev/null || true
exit 0
