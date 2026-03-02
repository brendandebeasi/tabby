#!/usr/bin/env bash

set -eu

if [ "$(tmux show-option -gqv @tabby_spawning 2>/dev/null)" = "1" ]; then
    exit 0
fi

LOG="/tmp/tabby-focus.log"
TS=$(date +%s 2>/dev/null || echo "")
printf "%s restore_input_focus start win=%s pane=%s\n" "$TS" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" >> "$LOG"

SESSION_ID="${1:-}"
if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")
fi
if [ -z "$SESSION_ID" ]; then
    exit 0
fi

current_window=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
if [ -z "$current_window" ]; then
    current_window=$(tmux list-windows -t "$SESSION_ID" -F "#{window_id}" 2>/dev/null | head -1)
fi

current_pane=$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo "")
current_cmd=""
if [ -n "$current_pane" ]; then
    current_cmd=$(tmux display-message -p -t "$current_pane" '#{pane_current_command}' 2>/dev/null || echo "")
fi

target_pane=""
case "$current_cmd" in
    sidebar|sidebar-renderer|pane-header|tabbar|pane-bar)
        ;;
    *)
        if [ -n "$current_pane" ]; then
            target_pane="$current_pane"
        fi
        ;;
esac

if [ -z "$target_pane" ] && [ -n "$current_window" ]; then
    while IFS='|' read -r pane_id pane_cmd; do
        [ -z "$pane_id" ] && continue
        case "$pane_cmd" in
            sidebar|sidebar-renderer|pane-header|tabbar|pane-bar)
                continue
                ;;
            *)
                target_pane="$pane_id"
                break
                ;;
        esac
    done < <(tmux list-panes -t "$current_window" -F "#{pane_id}|#{pane_current_command}" 2>/dev/null || true)
fi

if [ -z "$target_pane" ]; then
    while IFS='|' read -r pane_id pane_cmd; do
        [ -z "$pane_id" ] && continue
        case "$pane_cmd" in
            sidebar|sidebar-renderer|pane-header|tabbar|pane-bar)
                continue
                ;;
            *)
                target_pane="$pane_id"
                break
                ;;
        esac
    done < <(tmux list-panes -t "$SESSION_ID" -F "#{pane_id}|#{pane_current_command}" 2>/dev/null || true)
fi

if [ -n "$target_pane" ]; then
    target_window=$(tmux display-message -p -t "$target_pane" '#{window_id}' 2>/dev/null || echo "")
    if [ -n "$target_window" ]; then
        tmux select-window -t "$target_window" 2>/dev/null || true
    fi
    tmux select-pane -t "$target_pane" 2>/dev/null || true
fi

tmux set -g mouse off 2>/dev/null || true
sleep 0.05
tmux set -g mouse on 2>/dev/null || true

for client_tty in $(tmux list-clients -F "#{client_tty}" 2>/dev/null || true); do
    tmux refresh-client -t "$client_tty" -S 2>/dev/null || true
done
