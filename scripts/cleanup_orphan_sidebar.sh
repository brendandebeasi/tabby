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

cleanup_window_if_orphan() {
    local target_window="$1"
    [ -z "$target_window" ] && return 0

    for _ in 1 2 3 4 5; do
        local panes
        panes=$(tmux list-panes -t "$target_window" -F "#{pane_dead}|#{pane_current_command}|#{pane_start_command}" 2>/dev/null || true)
        [ -z "$panes" ] && return 0

        local main_panes
        main_panes=$(printf "%s\n" "$panes" | awk -F'|' '
            $1 != "1" {
                cmd = $2 " " $3
                if (cmd !~ /(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)/) {
                    count++
                }
            }
            END { print count+0 }
        ')

        if [ "$main_panes" -eq 0 ]; then
            local current_window
            current_window=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
            if [ -n "$current_window" ] && [ "$current_window" = "$target_window" ]; then
                tmux run-shell "$CURRENT_DIR/scripts/select_previous_window.sh" 2>/dev/null || true
            fi
            tmux kill-window -t "$target_window" 2>/dev/null || true
            return 0
        fi

        local dead_panes
        dead_panes=$(printf "%s\n" "$panes" | awk -F'|' '$1 == "1" { count++ } END { print count+0 }')
        [ "$dead_panes" -eq 0 ] && return 0
        sleep 0.05
    done
}

if [ -n "$WINDOW_ID" ]; then
    cleanup_window_if_orphan "$WINDOW_ID"
fi

if [ -n "$SESSION_ID" ]; then
    while IFS= read -r wid; do
        [ -z "$wid" ] && continue
        [ "$wid" = "$WINDOW_ID" ] && continue
        cleanup_window_if_orphan "$wid"
    done < <(tmux list-windows -t "$SESSION_ID" -F "#{window_id}" 2>/dev/null || true)
fi

if [ -n "$SESSION_ID" ]; then
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        PID="$(cat "$DAEMON_PID_FILE" 2>/dev/null || true)"
        if [ -n "$PID" ]; then
            kill -USR1 "$PID" 2>/dev/null || true
        fi
    fi
fi
