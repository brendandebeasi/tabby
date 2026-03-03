#!/usr/bin/env bash
CLOSED_INDEX="${1:-}"

ALLOW_SELECT=$(tmux show-option -gqv @tabby_close_select_window 2>/dev/null || echo "")
ALLOW_INDEX=$(tmux show-option -gqv @tabby_close_select_index 2>/dev/null || echo "")

PENDING_NEW=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
if [ -n "$PENDING_NEW" ] && tmux list-windows -F '#{window_id}' 2>/dev/null | grep -qFx "$PENDING_NEW"; then
    exit 0
fi

WINDOW_ROWS=$(tmux list-windows -F '#{window_index}|#{window_id}' 2>/dev/null || true)
[ -z "$WINDOW_ROWS" ] && exit 0

pick_above_or_next() {
    local idx="$1"
    local best_above_idx=-999999
    local best_above_id=""
    local best_below_idx=999999
    local best_below_id=""
    local row widx wid
    while IFS='|' read -r widx wid; do
        [ -z "$widx" ] && continue
        [ -z "$wid" ] && continue
        case "$widx" in
            ''|*[!0-9]*) continue ;;
        esac
        if [ "$widx" -lt "$idx" ] && [ "$widx" -gt "$best_above_idx" ]; then
            best_above_idx="$widx"
            best_above_id="$wid"
        fi
        if [ "$widx" -gt "$idx" ] && [ "$widx" -lt "$best_below_idx" ]; then
            best_below_idx="$widx"
            best_below_id="$wid"
        fi
    done <<EOF
$WINDOW_ROWS
EOF

    if [ -n "$best_above_id" ]; then
        printf "%s\n" "$best_above_id"
        return 0
    fi
    if [ -n "$best_below_id" ]; then
        printf "%s\n" "$best_below_id"
        return 0
    fi
    return 1
}

TARGET_ID=""
if [ "$ALLOW_SELECT" = "1" ] && [ -n "$CLOSED_INDEX" ] && [ "$CLOSED_INDEX" = "$ALLOW_INDEX" ]; then
    case "$CLOSED_INDEX" in
        ''|*[!0-9]*) ;;
        *) TARGET_ID=$(pick_above_or_next "$CLOSED_INDEX" || true) ;;
    esac
fi

if [ -n "$TARGET_ID" ]; then
    tmux select-window -t "$TARGET_ID" 2>/dev/null || true
fi

if [ -z "$TARGET_ID" ]; then
    HISTORY=$(tmux show-option -gqv @tabby_window_history 2>/dev/null || echo "")
    if [ -n "$HISTORY" ]; then
        EXISTING=$(tmux list-windows -F '#{window_id}' 2>/dev/null || true)
        IFS=',' read -ra ITEMS <<< "$HISTORY"
        CLEANED=""
        for item in "${ITEMS[@]}"; do
            [ -z "$item" ] && continue
            if echo "$EXISTING" | grep -qF "$item"; then
                if [ -z "$CLEANED" ]; then
                    CLEANED="$item"
                else
                    CLEANED="$CLEANED,$item"
                fi
            fi
        done
        tmux set-option -g @tabby_window_history "$CLEANED"
    fi
fi

exit 0
