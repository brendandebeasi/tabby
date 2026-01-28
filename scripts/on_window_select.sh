#!/usr/bin/env bash
# Handler for window selection - signal daemon and update border color

# Clear AI tool input/bell indicators when user switches to a window
# (user is now looking at it, so the notification is acknowledged)
tmux set-option -w @tabby_input "" 2>/dev/null || true
tmux set-option -w @tabby_bell "" 2>/dev/null || true

# Signal daemon to refresh immediately
DAEMON_PID_FILE="/tmp/tabby-daemon-$(tmux display-message -p '#{session_id}').pid"
[ -f "$DAEMON_PID_FILE" ] && kill -USR1 "$(cat "$DAEMON_PID_FILE")" 2>/dev/null || true

# Update pane border color if border_from_tab is enabled
BORDER_FROM_TAB=$(tmux show-option -gqv @tabby_border_from_tab)
if [ "$BORDER_FROM_TAB" = "true" ]; then
    # Get the window's tab bg color (set by sidebar via @tabby_pane_active)
    # Note: show-window-option returns quotes around values, so strip them
    TAB_BG=$(tmux show-window-option @tabby_pane_active 2>/dev/null | awk '{print $2}' | tr -d '"')

    # If no custom bg color, use the default
    if [ -z "$TAB_BG" ]; then
        TAB_BG=$(tmux show-option -gqv @tabby_pane_active_bg_default)
    fi

    # Get the tab fg color
    TAB_FG=$(tmux show-option -gqv @tabby_pane_active_fg)
    if [ -z "$TAB_FG" ]; then
        TAB_FG="#ffffff"
    fi

    # BOTH borders use SAME color to prevent any half/half on shared edges
    if [ -n "$TAB_BG" ]; then
        tmux set-option -g pane-active-border-style "fg=$TAB_BG"
        tmux set-option -g pane-border-style "fg=$TAB_BG"
    fi
fi

# Always exit success
exit 0
