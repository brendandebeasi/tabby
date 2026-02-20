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

for _ in 1 2 3 4 5; do
    PANES=$(tmux list-panes -t "$WINDOW_ID" -F "#{pane_dead}|#{pane_current_command}|#{pane_start_command}" 2>/dev/null || true)
    [ -z "$PANES" ] && exit 0

    MAIN_PANES=$(printf "%s\n" "$PANES" | awk -F'|' '
        $1 != "1" {
            cmd = $2 " " $3
            if (cmd !~ /(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)/) {
                count++
            }
        }
        END { print count+0 }
    ')

    if [ "$MAIN_PANES" -eq 0 ]; then
        CURRENT_WINDOW=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
        if [ -n "$CURRENT_WINDOW" ] && [ "$CURRENT_WINDOW" = "$WINDOW_ID" ]; then
            tmux run-shell "$CURRENT_DIR/scripts/select_previous_window.sh" 2>/dev/null || true
        fi
        tmux kill-window -t "$WINDOW_ID" 2>/dev/null || true
        break
    fi

    DEAD_PANES=$(printf "%s\n" "$PANES" | awk -F'|' '$1 == "1" { count++ } END { print count+0 }')
    [ "$DEAD_PANES" -eq 0 ] && break
    sleep 0.05
done

if [ -n "$SESSION_ID" ]; then
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        read -r PID < "$DAEMON_PID_FILE"
        kill -USR1 "$PID" 2>/dev/null || true
    fi
fi
