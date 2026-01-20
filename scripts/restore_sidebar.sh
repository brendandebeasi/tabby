#!/usr/bin/env bash
# Restore sidebar/tabbar state when client attaches to session
# Uses tmux user option @tmux-tabs-sidebar for persistence across reattach

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Check tmux user option for persistent state (survives detach/reattach)
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")

# Also check temp file as fallback
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
    # Vertical sidebar mode
    # Note: grep -c outputs count (0 if no match) but exits 1 on no match
    SIDEBAR_COUNT=$(tmux list-panes -s -F "#{pane_current_command}" 2>/dev/null | grep -c "^sidebar$" || true)

    if [ "$SIDEBAR_COUNT" -eq 0 ]; then
        # Restore sidebars in all windows
        CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

        tmux list-windows -F "#{window_id}" | while read -r window_id; do
            if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"; then
                tmux split-window -t "$window_id" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" 2>/dev/null || true
            fi
        done

        # Return to original window and focus main pane
        tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
        tmux select-pane -t "{right}" 2>/dev/null || true
    fi
elif [ "$MODE" = "horizontal" ]; then
    # Horizontal tabbar mode
    tmux set-option -g status off

    # Note: grep -c outputs count (0 if no match) but exits 1 on no match
    TABBAR_COUNT=$(tmux list-panes -s -F "#{pane_current_command}" 2>/dev/null | grep -c "^tabbar$" || true)

    if [ "$TABBAR_COUNT" -eq 0 ]; then
        # Restore tabbars in all windows
        CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

        tmux list-windows -F "#{window_id}" | while read -r window_id; do
            if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^tabbar$"; then
                tmux split-window -t "$window_id" -v -b -l 1 "exec \"$CURRENT_DIR/bin/tabbar\"" 2>/dev/null || true
            fi
        done

        # Return to original window and focus main pane
        tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
        tmux select-pane -t "{down}" 2>/dev/null || true
    fi
fi
