#!/usr/bin/env bash
set -u

NEW_ID="${1:-}"
if [ -z "$NEW_ID" ]; then
    exit 0
fi

is_aux_cmd() {
    case "$1" in
        *sidebar*|*renderer*|*pane-header*|*tabby-daemon*) return 0 ;;
        *) return 1 ;;
    esac
}

pick_content_pane() {
    local pane cmd startcmd active
    while IFS='|' read -r pane cmd startcmd active; do
        [ -z "$pane" ] && continue
        if ! is_aux_cmd "$cmd" && ! is_aux_cmd "$startcmd"; then
            if [ "$active" = "1" ]; then
                printf "%s\n" "$pane"
                return 0
            fi
        fi
    done < <(tmux list-panes -t "$NEW_ID" -F "#{pane_id}|#{pane_current_command}|#{pane_start_command}|#{pane_active}" 2>/dev/null)

    while IFS='|' read -r pane cmd startcmd active; do
        [ -z "$pane" ] && continue
        if ! is_aux_cmd "$cmd" && ! is_aux_cmd "$startcmd"; then
            printf "%s\n" "$pane"
            return 0
        fi
    done < <(tmux list-panes -t "$NEW_ID" -F "#{pane_id}|#{pane_current_command}|#{pane_start_command}|#{pane_active}" 2>/dev/null)
    return 1
}

PENDING_NEW=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
if [ "$PENDING_NEW" != "$NEW_ID" ]; then
    exit 0
fi

tmux select-window -t "$NEW_ID" 2>/dev/null || true
CONTENT_PANE=$(pick_content_pane || true)
[ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true

sleep 0.15

PENDING_NEW=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
CURRENT_WIN=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
if [ "$PENDING_NEW" = "$NEW_ID" ] && [ "$CURRENT_WIN" = "$NEW_ID" ]; then
    CONTENT_PANE=$(pick_content_pane || true)
    [ -n "$CONTENT_PANE" ] && tmux select-pane -t "$CONTENT_PANE" 2>/dev/null || true
    tmux set-option -g @tabby_last_window "$NEW_ID" 2>/dev/null || true
    [ -n "$CONTENT_PANE" ] && tmux set-option -g @tabby_last_pane "$CONTENT_PANE" 2>/dev/null || true
fi

PENDING_NEW=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
if [ "$PENDING_NEW" = "$NEW_ID" ]; then
    tmux set-option -gu @tabby_new_window_id 2>/dev/null || true
fi

LOG="/tmp/tabby-focus.log"
TS=$(date +%s 2>/dev/null || echo "")
printf "%s focus_new_window id=%s win=%s pane=%s\n" "$TS" "$NEW_ID" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" >> "$LOG"

exit 0
