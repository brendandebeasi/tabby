#!/usr/bin/env bash
set -u

CLIENT_TTY="${1:-}"

tmux set-option -g @tabby_spawning 1 2>/dev/null || true

LOG="/tmp/tabby-focus.log"
TS=$(date +%s 2>/dev/null || echo "")
printf "%s new_window start win=%s pane=%s\n" "$TS" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" >> "$LOG"

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
if [ -z "$CLIENT_SESSION_ID" ]; then
    NEW_WINDOW_ARGS=(new-window -d -P -F "#{window_id}")
fi
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
    TS=$(date +%s 2>/dev/null || echo "")
    printf "%s new_window id=%s\n" "$TS" "$NEW_WINDOW_ID" >> "$LOG"

    if [ -n "$CLIENT_TTY" ]; then
        tmux switch-client -c "$CLIENT_TTY" -t "$NEW_WINDOW_ID" 2>/dev/null || tmux select-window -t "$NEW_WINDOW_ID" 2>/dev/null || true
    else
        tmux select-window -t "$NEW_WINDOW_ID" 2>/dev/null || true
    fi
    CONTENT_PANE=$(tmux list-panes -t "$NEW_WINDOW_ID" -F "#{pane_id}|#{pane_current_command}|#{pane_start_command}" 2>/dev/null | awk -F'|' '$2 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $3 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ {print $1; exit}')
    [ -z "$CONTENT_PANE" ] && CONTENT_PANE=$(tmux list-panes -t "$NEW_WINDOW_ID" -F "#{pane_id}" 2>/dev/null | head -1)
    [ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true
    tmux set-option -g @tabby_last_window "$NEW_WINDOW_ID" 2>/dev/null || true
    [ -n "$CONTENT_PANE" ] && tmux set-option -g @tabby_last_pane "$CONTENT_PANE" 2>/dev/null || true
fi

tmux set-option -gu @tabby_new_window_group 2>/dev/null || true
tmux set-option -gu @tabby_new_window_path 2>/dev/null || true

# Signal daemon so it can spawn renderers for the new window.
# Keep @tabby_spawning=1 so daemon cleanup skips this window.
PID_FILE="/tmp/tabby-daemon-$(tmux display-message -p '#{session_id}').pid"
[ -f "$PID_FILE" ] && kill -USR1 "$(cat "$PID_FILE")" 2>/dev/null || true

if [ -n "$NEW_WINDOW_ID" ]; then
    sleep 0.08
    if [ -n "$CLIENT_TTY" ]; then
        tmux switch-client -c "$CLIENT_TTY" -t "$NEW_WINDOW_ID" 2>/dev/null || tmux select-window -t "$NEW_WINDOW_ID" 2>/dev/null || true
    else
        tmux select-window -t "$NEW_WINDOW_ID" 2>/dev/null || true
    fi
    CONTENT_PANE=$(tmux list-panes -t "$NEW_WINDOW_ID" -F "#{pane_id}|#{pane_current_command}|#{pane_start_command}" 2>/dev/null | awk -F'|' '$2 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $3 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ {print $1; exit}')
    [ -z "$CONTENT_PANE" ] && CONTENT_PANE=$(tmux list-panes -t "$NEW_WINDOW_ID" -F "#{pane_id}" 2>/dev/null | head -1)
    [ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true
    tmux set-option -g @tabby_last_window "$NEW_WINDOW_ID" 2>/dev/null || true
    [ -n "$CONTENT_PANE" ] && tmux set-option -g @tabby_last_pane "$CONTENT_PANE" 2>/dev/null || true

    # Clear spawning flag AFTER second focus attempt so daemon cleanup
    # cannot steal focus or kill the new window during the sleep+refocus.
    tmux set-option -gu @tabby_spawning 2>/dev/null || true
    ( sleep 1.2; PENDING=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo ""); [ "$PENDING" = "$NEW_WINDOW_ID" ] && tmux set-option -gu @tabby_new_window_id 2>/dev/null || true ) >/dev/null 2>&1 &
fi
# Safety: ensure spawning flag is always cleared (even if NEW_WINDOW_ID was empty)
tmux set-option -gu @tabby_spawning 2>/dev/null || true
TS=$(date +%s 2>/dev/null || echo "")
printf "%s new_window end win=%s pane=%s\n" "$TS" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" >> "$LOG"

exit 0

exit 0

