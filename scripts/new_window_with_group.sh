#!/usr/bin/env bash
set -u

CLIENT_TTY="${1:-}"
CURRENT_DIR="/Users/b/git"

SAVED_GROUP=$(tmux show-option -gqv @tabby_new_window_group 2>/dev/null || echo "")
SAVED_PATH=$(tmux show-option -gqv @tabby_new_window_path 2>/dev/null || echo "")
CLIENT_SESSION_ID=""

if [ -n "$CLIENT_TTY" ]; then
    CLIENT_SESSION_ID=$(tmux display-message -p -c "$CLIENT_TTY" "#{session_id}" 2>/dev/null || echo "")
fi

if [ -z "$SAVED_GROUP" ]; then
    if [ -n "$CLIENT_TTY" ]; then
        SAVED_GROUP=$(tmux display-message -p -c "$CLIENT_TTY" "#{@tabby_group}" 2>/dev/null || echo "")
    fi
    if [ -z "$SAVED_GROUP" ]; then
        SAVED_GROUP=$(tmux show-window-options -v @tabby_group 2>/dev/null || echo "")
    fi
fi

if [ -z "$SAVED_PATH" ]; then
    if [ -n "$CLIENT_TTY" ]; then
        SAVED_PATH=$(tmux display-message -p -c "$CLIENT_TTY" "#{pane_current_path}" 2>/dev/null || echo "")
    fi
    if [ -z "$SAVED_PATH" ]; then
        SAVED_PATH=$(tmux display-message -p "#{pane_current_path}" 2>/dev/null || echo "")
    fi
fi

NEW_WINDOW_ARGS=(new-window -P -F "#{window_id}")
if [ -n "$CLIENT_SESSION_ID" ]; then
    NEW_WINDOW_ARGS+=( -t "${CLIENT_SESSION_ID}:" )
fi
if [ -n "$SAVED_PATH" ]; then
    NEW_WINDOW_ARGS+=( -c "$SAVED_PATH" )
fi
NEW_WINDOW_ID=$(tmux "${NEW_WINDOW_ARGS[@]}" 2>/dev/null || true)
NEW_WINDOW_ID=$(printf "%s" "$NEW_WINDOW_ID" | tr -d '\r\n')

if [ -n "$NEW_WINDOW_ID" ] && [ -n "$SAVED_GROUP" ] && [ "$SAVED_GROUP" != "Default" ]; then
    tmux set-window-option -t "$NEW_WINDOW_ID" @tabby_group "$SAVED_GROUP" 2>/dev/null || true
fi

if [ -n "$NEW_WINDOW_ID" ]; then
    tmux set-option -g @tabby_new_window_id "$NEW_WINDOW_ID" 2>/dev/null || true
    tmux select-window -t "$NEW_WINDOW_ID" 2>/dev/null || true
    "$CURRENT_DIR/scripts/focus_new_window.sh" "$NEW_WINDOW_ID" >/dev/null 2>&1 &
    ( sleep 2; PENDING=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo ""); [ "$PENDING" = "$NEW_WINDOW_ID" ] && tmux set-option -gu @tabby_new_window_id 2>/dev/null || true ) &
fi

tmux set-option -gu @tabby_new_window_group 2>/dev/null || true
tmux set-option -gu @tabby_new_window_path 2>/dev/null || true

exit 0
