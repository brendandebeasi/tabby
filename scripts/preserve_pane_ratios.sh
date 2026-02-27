#!/usr/bin/env bash
# Restore saved pane layout after a pane exits to preserve size ratios.
# Called from after-kill-pane hook. Without this, tmux equalizes pane sizes
# after a pane closes, which disrupts the user's layout.
#
# Usage: preserve_pane_ratios.sh [window_id]
# If window_id is passed, use it directly; otherwise query tmux.

WINDOW_ID="${1:-$(tmux display-message -p '#{window_id}' 2>/dev/null)}"
[ -z "$WINDOW_ID" ] && exit 0

# Check if we have a saved layout for this window
SAVED_LAYOUT=$(tmux show-option -gqv "@tabby_layout_${WINDOW_ID}" 2>/dev/null)
[ -z "$SAVED_LAYOUT" ] && exit 0

# Only attempt restore if more than one pane remains
PANE_COUNT=$(tmux list-panes -t "$WINDOW_ID" 2>/dev/null | wc -l | tr -d ' ')
[ "$PANE_COUNT" -le 1 ] && exit 0

# Apply the saved layout (may fail if pane count changed too much, that's fine).
# tmux select-layout intelligently redistributes space even with fewer panes.
tmux select-layout -t "$WINDOW_ID" "$SAVED_LAYOUT" 2>/dev/null || true

# Don't clear the saved layout immediately — it might be needed again when
# the orphaned header pane is cleaned up shortly after. The layout will be
# overwritten next time save_pane_layout.sh runs (on pane select, split, etc.).
exit 0
