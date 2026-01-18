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
else
    # No sidebar - open one and mark as enabled
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux split-window -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\""
    
    # Open sidebar in all existing windows
    tmux list-windows -F "#{window_id}" | while read -r window_id; do
        if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" | grep -q "^sidebar$"; then
            tmux split-window -t "$window_id" -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" 2>/dev/null || true
        fi
    done
fi

exit 0
