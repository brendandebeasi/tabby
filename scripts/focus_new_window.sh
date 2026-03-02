#!/usr/bin/env bash
set -u

NEW_ID="${1:-}"
if [ -z "$NEW_ID" ]; then
    exit 0
fi

# Brief delay for renderer/header spawning, then ensure focus on content pane.
# Renderer spawns with -d (no focus steal), so one check after spawn settles is enough.
sleep 0.15
tmux select-window -t "$NEW_ID" 2>/dev/null || true
CONTENT_PANE=$(tmux list-panes -t "$NEW_ID" -F "#{pane_id}" 2>/dev/null | tail -1)
[ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true

# Second check after header spawning settles
sleep 0.2
CONTENT_PANE=$(tmux list-panes -t "$NEW_ID" -F "#{pane_id}" 2>/dev/null | tail -1)
[ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true

tmux set-option -gu @tabby_new_window_id 2>/dev/null || true

LOG="/tmp/tabby-focus.log"
TS=$(date +%s 2>/dev/null || echo "")
printf "%s focus_new_window id=%s win=%s pane=%s\n" "$TS" "$NEW_ID" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" >> "$LOG"

exit 0
