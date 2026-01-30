#!/usr/bin/env bash
PANE_ID="$1"
MOUSE_X="$2"
MOUSE_Y="$3"

# Debug log
echo "$(date): PANE_ID='$PANE_ID' X='$MOUSE_X' Y='$MOUSE_Y'" >> /tmp/click-debug.log

# Fallback if format wasn't expanded
if [[ -z "$PANE_ID" ]] || [[ "$PANE_ID" == *"mouse_pane"* ]]; then
    echo "$(date): Format not expanded, using active pane" >> /tmp/click-debug.log
    PANE_ID=$(tmux display-message -p "#{pane_id}")
fi

PANE_CMD=$(tmux display-message -t "$PANE_ID" -p "#{pane_current_command}" 2>/dev/null)
echo "$(date): PANE_CMD='$PANE_CMD'" >> /tmp/click-debug.log

if [[ "$PANE_CMD" == "pane-header" ]]; then
    tmux set-option -g @tabby_last_click_x "$MOUSE_X"
    tmux set-option -g @tabby_last_click_y "$MOUSE_Y"
    tmux set-option -g @tabby_last_click_pane "$PANE_ID"
    tmux select-pane -t "$PANE_ID"
    echo "$(date): Handled as pane-header" >> /tmp/click-debug.log
else
    tmux select-pane -t "$PANE_ID"
    echo "$(date): Selected pane" >> /tmp/click-debug.log
fi
