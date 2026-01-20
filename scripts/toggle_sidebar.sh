#!/usr/bin/env bash
# Toggle tmux-tabs sidebar
# Fixes: BUG-001 (PID file race), BUG-002 (pane scope), BUG-006 (process tracking)

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Use session-based identifier instead of $$ (shell PID)
# This ensures consistent identification across script invocations
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

# Search across the ENTIRE SESSION for sidebar panes (not just current window)
# This prevents multiple sidebars from spawning across windows
find_sidebar_pane() {
    # Don't use pipefail here - grep returns 1 when no match found
    tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | \
        grep "^sidebar|" 2>/dev/null | \
        cut -d'|' -f2 | \
        head -1 || echo ""
}

SIDEBAR_PANE=$(find_sidebar_pane)

if [ -n "$SIDEBAR_PANE" ]; then
    # Sidebar exists - close all sidebars in session
    tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" | \
        grep "^sidebar|" | \
        cut -d'|' -f2 | \
        while read -r pane_id; do
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
        done
    echo "disabled" > "$SIDEBAR_STATE_FILE"
    # Persist to tmux option (survives detach/reattach)
    tmux set-option @tmux-tabs-sidebar "disabled"
    # Re-enable tmux status bar when sidebar is disabled
    tmux set-option -g status on
else
    # No sidebar - open one in EVERY window so switching windows keeps sidebar visible
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    # Persist to tmux option (survives detach/reattach)
    tmux set-option @tmux-tabs-sidebar "enabled"

    # Close any tabbar panes first
    tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | \
        grep "^tabbar|" | \
        cut -d'|' -f2 | \
        while read -r pane_id; do
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
        done

    # Disable tmux status bar (sidebar provides navigation)
    tmux set-option -g status off

    # Get current window to return to it after
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

    # Open sidebar in all existing windows
    tmux list-windows -F "#{window_id}" | while read -r window_id; do
        # Check if this window already has a sidebar pane
        if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"; then
            # Get the first pane in the window to split from (ensures sidebar is always on the left)
            FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" | head -1)
            tmux split-window -t "$FIRST_PANE" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" 2>/dev/null || true
        fi
    done

    # Return to original window and focus the main pane (pane 1 = first non-sidebar pane)
    tmux select-window -t "$CURRENT_WINDOW"
    tmux select-pane -t :.1
fi

exit 0
