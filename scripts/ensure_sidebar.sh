#!/usr/bin/env bash
# Ensure sidebar/tabbar exists in current window when that mode is enabled
# Called by tmux hooks when windows are created/switched

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Get mode from tmux option or state file
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
    # Check if CURRENT window has a sidebar (not session-wide)
    # Note: grep -c outputs the count (0 if no match) but exits 1 on no match
    # Using || true to suppress the exit code without adding extra output
    SIDEBAR_IN_WINDOW=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -c "^sidebar$" || true)

    if [ "$SIDEBAR_IN_WINDOW" -eq 0 ]; then
        # No sidebar in current window - add one
        # Get the first pane in the window (leftmost) to split from
        FIRST_PANE=$(tmux list-panes -F "#{pane_id}" | head -1)
        # Split from the first pane to ensure sidebar is always on the left
        tmux split-window -t "$FIRST_PANE" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\""
        # Return focus to main pane (pane 1, which is now the original first pane)
        tmux select-pane -t :.1
    fi
elif [ "$MODE" = "horizontal" ]; then
    # Check if CURRENT window has a tabbar
    # Note: grep -c outputs the count (0 if no match) but exits 1 on no match
    TABBAR_IN_WINDOW=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -c "^tabbar$" || true)

    if [ "$TABBAR_IN_WINDOW" -eq 0 ]; then
        # Get the first pane in the window to split from (ensures tabbar is always at top)
        FIRST_PANE=$(tmux list-panes -F "#{pane_id}" | head -1)
        # No tabbar in current window - add one at top (2 lines for tabs + panes)
        tmux split-window -t "$FIRST_PANE" -v -b -l 2 "exec \"$CURRENT_DIR/bin/tabbar\""
        # Return focus to main pane (pane 1, the original first pane)
        tmux select-pane -t :.1
    fi
fi
