#!/usr/bin/env bash
# Switch to horizontal tab bar mode (using tabbar pane at top)

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
TABBAR_HEIGHT=2  # 2 lines: 1 for tabs, 1 for panes (shown when >1 pane)

# Close all sidebar panes in the session
tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | \
    grep "^sidebar|" | \
    cut -d'|' -f2 | \
    while read -r pane_id; do
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done

# Mark sidebar as disabled
echo "horizontal" > "$SIDEBAR_STATE_FILE"
tmux set-option @tmux-tabs-sidebar "horizontal"

# Disable tmux's built-in status bar (we're using our own pane)
tmux set-option -g status off

# Open tabbar pane at top of each window
tmux list-windows -F "#{window_id}" | while read -r window_id; do
    # Check if tabbar already exists in this window
    if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^tabbar$"; then
        # Get the first pane in the window to split from (ensures tabbar is always at top)
        FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" | head -1)
        # Split at top with height for tabs + panes
        tmux split-window -t "$FIRST_PANE" -v -b -l "$TABBAR_HEIGHT" "exec \"$CURRENT_DIR/bin/tabbar\"" 2>/dev/null || true
    fi
done

tmux refresh-client -S

tmux display-message "Horizontal tabs mode (pane)"
