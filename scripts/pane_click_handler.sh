#!/usr/bin/env bash
# Handle click on pane-header panes. Store click position and select pane to trigger FocusMsg.
# The pane-header's BubbleTea receives FocusMsg, reads stored position, sends to daemon.
# Daemon handles all button logic (single source of truth).
PANE_ID="$1"
MOUSE_X="$2"
MOUSE_Y="$3"
PANE_LEFT="$4"
PANE_TOP="$5"

# Convert window-absolute mouse coordinates to pane-local coordinates.
# BubbleTea hit testing uses local pane coordinates.
LOCAL_X=$((MOUSE_X - PANE_LEFT))
LOCAL_Y=$((MOUSE_Y - PANE_TOP))

if [ "$LOCAL_X" -lt 0 ]; then
    LOCAL_X=0
fi
if [ "$LOCAL_Y" -lt 0 ]; then
    LOCAL_Y=0
fi

# Store click position for pane-header to read on focus gain
tmux set-option -g @tabby_last_click_x "$LOCAL_X"
tmux set-option -g @tabby_last_click_y "$LOCAL_Y"
tmux set-option -g @tabby_last_click_pane "$PANE_ID"

tmux select-pane -t "$PANE_ID"
