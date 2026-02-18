#!/usr/bin/env bash
# Handle click on pane-header panes. Store click position and select pane to trigger FocusMsg.
# The pane-header's BubbleTea receives FocusMsg, reads stored position, sends to daemon.
# Daemon handles all button logic (single source of truth).
PANE_ID="$1"
MOUSE_X="$2"
MOUSE_Y="$3"

# Store click position for pane-header to read on focus gain
tmux set-option -g @tabby_last_click_x "$MOUSE_X"
tmux set-option -g @tabby_last_click_y "$MOUSE_Y"
tmux set-option -g @tabby_last_click_pane "$PANE_ID"

IS_ACTIVE=$(tmux display-message -p -t "$PANE_ID" "#{pane_active}" 2>/dev/null || echo "0")
if [ "$IS_ACTIVE" = "1" ]; then
    tmux send-keys -M -t "$PANE_ID"
else
    tmux select-pane -t "$PANE_ID"
fi
