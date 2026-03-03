#!/usr/bin/env bash

WINDOW_INDEX="${1:-}"

case "$WINDOW_INDEX" in
    ''|*[!0-9]*)
        exit 1
        ;;
esac

TARGET_ID=$(tmux list-windows -F '#{window_index}|#{window_id}' 2>/dev/null | awk -F'|' -v i="$WINDOW_INDEX" '$1==i {print $2; exit}')
[ -z "$TARGET_ID" ] && exit 0

ABOVE_ID=$(tmux list-windows -F '#{window_index}|#{window_id}' 2>/dev/null | awk -F'|' -v i="$WINDOW_INDEX" '$1<i {print $1"|"$2}' | sort -t'|' -k1,1nr | head -1 | cut -d'|' -f2)
if [ -z "$ABOVE_ID" ]; then
    ABOVE_ID=$(tmux list-windows -F '#{window_index}|#{window_id}' 2>/dev/null | awk -F'|' -v i="$WINDOW_INDEX" '$1>i {print $1"|"$2}' | sort -t'|' -k1,1n | head -1 | cut -d'|' -f2)
fi

ACTIVE_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
if [ "$ACTIVE_ID" = "$TARGET_ID" ] && [ -n "$ABOVE_ID" ]; then
    tmux select-window -t "$ABOVE_ID" 2>/dev/null || true
fi

tmux set-option -g @tabby_close_select_window 1 2>/dev/null || true
tmux set-option -g @tabby_close_select_index "$WINDOW_INDEX" 2>/dev/null || true
tmux kill-window -t "$TARGET_ID" 2>/dev/null || true
( sleep 0.2; tmux set-option -gu @tabby_close_select_window 2>/dev/null || true; tmux set-option -gu @tabby_close_select_index 2>/dev/null || true ) >/dev/null 2>&1 &
exit 0
