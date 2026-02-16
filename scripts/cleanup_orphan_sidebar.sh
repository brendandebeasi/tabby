#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

SESSION_ID="${1:-}"
WINDOW_ID="${2:-}"

if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")
fi

if [ -z "$WINDOW_ID" ]; then
    WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
fi

[ -z "$WINDOW_ID" ] && exit 0

sleep 0.05

PANES=$(tmux list-panes -t "$WINDOW_ID" -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null || true)
[ -z "$PANES" ] && exit 0

MAIN_PANES=$(printf "%s\n" "$PANES" | grep -cvE "(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)" || true)

if [ "$MAIN_PANES" -eq 0 ]; then
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
    if [ -n "$CURRENT_WINDOW" ] && [ "$CURRENT_WINDOW" = "$WINDOW_ID" ]; then
        tmux run-shell "$CURRENT_DIR/scripts/select_previous_window.sh" 2>/dev/null || true
    fi
    tmux kill-window -t "$WINDOW_ID" 2>/dev/null || true
fi

if [ -n "$SESSION_ID" ]; then
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        read -r PID < "$DAEMON_PID_FILE"
        kill -USR1 "$PID" 2>/dev/null || true
    fi
fi
