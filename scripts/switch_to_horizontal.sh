#!/usr/bin/env bash
# Switch to horizontal tab bar mode (using tabbar pane at top)

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tabby-sidebar-${SESSION_ID}.state"
TABBAR_HEIGHT=2  # 2 lines: 1 for tabs, 1 for panes (shown when >1 pane)

# Get current window to return to (before any pane changes)
CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

# Renumber windows to ensure sequential indices (0, 1, 2, ...)
tmux move-window -r 2>/dev/null || true

# Kill any sidebar panes first
while IFS= read -r line; do
    pane_id=$(echo "$line" | cut -d'|' -f2)
    tmux kill-pane -t "$pane_id" 2>/dev/null || true
done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^sidebar" || true)

# Mark sidebar as disabled
echo "horizontal" > "$SIDEBAR_STATE_FILE"
tmux set-option @tabby_sidebar "horizontal"

# Disable tmux's built-in status bar (we're using our own pane)
tmux set-option -g status off

# Open tabbar pane at top of each window
while IFS= read -r window_id; do
    [ -z "$window_id" ] && continue
    # Check if tabbar already exists in this window
    if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^tabbar$"; then
        # Get the first pane in the window to split from
        FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -n "$FIRST_PANE" ]; then
            # Split at top with height for tabs + panes
            tmux split-window -t "$FIRST_PANE" -v -b -l "$TABBAR_HEIGHT" "exec \"$CURRENT_DIR/bin/tabbar\"" || true
        fi
    fi
done < <(tmux list-windows -F "#{window_id}")

# Return to original window and focus main pane (below tabbar)
tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
tmux select-pane -t "{bottom}" 2>/dev/null || true

tmux refresh-client -S

