#!/usr/bin/env bash
# Update pane bar visibility based on active window's pane count
# Called by hooks when windows/panes change

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Check current mode - only run in horizontal mode
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ "$MODE" = "enabled" ]; then
    # Vertical sidebar mode - no pane bar needed
    exit 0
fi

# Count panes in active window (excluding sidebar/tabbar)
PANE_COUNT=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -cvE "^(sidebar|tabbar)$" || echo "0")

if [ "$PANE_COUNT" -gt 1 ]; then
    # Multiple panes - show 2 status lines
    tmux set-option -g status 2
    # Use quotes to protect brackets from shell interpretation
    tmux set-option -g "status-format[1]" "#($CURRENT_DIR/bin/render-pane-bar)"
else
    # Single pane or no panes - show 1 status line
    tmux set-option -g status 1
    tmux set-option -gu "status-format[1]" 2>/dev/null || true
fi
