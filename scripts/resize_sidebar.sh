#!/usr/bin/env bash
# Resize sidebar panes and maintain proportional pane ratios after terminal resize.
#
# BUG FIX: Previously this script read the CURRENT sidebar pane width
# (which tmux may have compressed to 1-2 columns) and saved that as the
# desired width. Now it reads the SAVED desired width and enforces it.

MIN_WIDTH=15

MOBILE_MAX_PERCENT=$(tmux show-option -gqv @tabby_sidebar_mobile_max_percent 2>/dev/null || echo "")
if [ -z "$MOBILE_MAX_PERCENT" ] || [ "$MOBILE_MAX_PERCENT" -lt 10 ] 2>/dev/null || [ "$MOBILE_MAX_PERCENT" -gt 60 ] 2>/dev/null; then
    MOBILE_MAX_PERCENT=20
fi

MOBILE_MIN_CONTENT_COLS=$(tmux show-option -gqv @tabby_sidebar_mobile_min_content_cols 2>/dev/null || echo "")
if [ -z "$MOBILE_MIN_CONTENT_COLS" ] || [ "$MOBILE_MIN_CONTENT_COLS" -lt 20 ] 2>/dev/null; then
    MOBILE_MIN_CONTENT_COLS=40
fi

MOBILE_MAX_WINDOW_COLS=$(tmux show-option -gqv @tabby_sidebar_mobile_max_window_cols 2>/dev/null || echo "")
if [ -z "$MOBILE_MAX_WINDOW_COLS" ] || [ "$MOBILE_MAX_WINDOW_COLS" -lt 60 ] 2>/dev/null; then
    MOBILE_MAX_WINDOW_COLS=110
fi

TABLET_MAX_WINDOW_COLS=$(tmux show-option -gqv @tabby_sidebar_tablet_max_window_cols 2>/dev/null || echo "")
if [ -z "$TABLET_MAX_WINDOW_COLS" ] || [ "$TABLET_MAX_WINDOW_COLS" -lt "$MOBILE_MAX_WINDOW_COLS" ] 2>/dev/null; then
    TABLET_MAX_WINDOW_COLS=170
fi

# Read the saved desired width (set by grow/shrink buttons or initial setup)
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width 2>/dev/null)
if [ -z "$SIDEBAR_WIDTH" ] || [ "$SIDEBAR_WIDTH" -lt "$MIN_WIDTH" ] 2>/dev/null; then
    SIDEBAR_WIDTH=25
fi

WIDTH_MOBILE=$(tmux show-option -gqv @tabby_sidebar_width_mobile 2>/dev/null || echo "")
if [ -z "$WIDTH_MOBILE" ] || [ "$WIDTH_MOBILE" -lt "$MIN_WIDTH" ] 2>/dev/null; then
    WIDTH_MOBILE=15
fi

WIDTH_TABLET=$(tmux show-option -gqv @tabby_sidebar_width_tablet 2>/dev/null || echo "")
if [ -z "$WIDTH_TABLET" ] || [ "$WIDTH_TABLET" -lt "$MIN_WIDTH" ] 2>/dev/null; then
    WIDTH_TABLET=20
fi

WIDTH_DESKTOP=$(tmux show-option -gqv @tabby_sidebar_width_desktop 2>/dev/null || echo "")
if [ -z "$WIDTH_DESKTOP" ] || [ "$WIDTH_DESKTOP" -lt "$MIN_WIDTH" ] 2>/dev/null; then
    WIDTH_DESKTOP="$SIDEBAR_WIDTH"
fi

# Apply saved width to all sidebar panes across all windows
tmux list-panes -a -F '#{pane_id}:#{pane_current_command}:#{pane_start_command}' 2>/dev/null | grep -E ':(sidebar|sidebar-renderer)' | while IFS=: read -r pane_id _; do
    TARGET_WIDTH="$SIDEBAR_WIDTH"
    CURRENT_WIDTH=$(tmux display-message -p -t "$pane_id" '#{pane_width}' 2>/dev/null || echo "")

    WINDOW_WIDTH=$(tmux display-message -p -t "$pane_id" '#{window_width}' 2>/dev/null || echo "")
    if [ -n "$WINDOW_WIDTH" ] && [ "$WINDOW_WIDTH" -gt 0 ] 2>/dev/null; then
        if [ "$WINDOW_WIDTH" -le "$MOBILE_MAX_WINDOW_COLS" ] 2>/dev/null; then
            TARGET_WIDTH="$WIDTH_MOBILE"
        elif [ "$WINDOW_WIDTH" -le "$TABLET_MAX_WINDOW_COLS" ] 2>/dev/null; then
            TARGET_WIDTH="$WIDTH_TABLET"
        else
            TARGET_WIDTH="$WIDTH_DESKTOP"
        fi
    fi

    if [ -n "$WINDOW_WIDTH" ] && [ "$WINDOW_WIDTH" -gt 0 ] 2>/dev/null && [ "$WINDOW_WIDTH" -le "$MOBILE_MAX_WINDOW_COLS" ] 2>/dev/null; then
        MAX_BY_FRACTION=$((WINDOW_WIDTH * MOBILE_MAX_PERCENT / 100))
        if [ "$MAX_BY_FRACTION" -lt "$MIN_WIDTH" ]; then
            MAX_BY_FRACTION=$MIN_WIDTH
        fi

        MAX_BY_CONTENT=$((WINDOW_WIDTH - MOBILE_MIN_CONTENT_COLS))
        if [ "$MAX_BY_CONTENT" -lt "$MIN_WIDTH" ]; then
            MAX_BY_CONTENT=$MIN_WIDTH
        fi

        MAX_REASONABLE="$MAX_BY_FRACTION"
        if [ "$MAX_BY_CONTENT" -lt "$MAX_REASONABLE" ]; then
            MAX_REASONABLE="$MAX_BY_CONTENT"
        fi

        if [ "$TARGET_WIDTH" -gt "$MAX_REASONABLE" ]; then
            TARGET_WIDTH="$MAX_REASONABLE"
        fi
    fi

    if [ -n "$CURRENT_WIDTH" ] && [ "$CURRENT_WIDTH" -ge "$MIN_WIDTH" ] 2>/dev/null; then
        if [ "$CURRENT_WIDTH" -lt "$TARGET_WIDTH" ]; then
            TARGET_WIDTH="$CURRENT_WIDTH"
        fi
    fi

    tmux resize-pane -t "$pane_id" -x "$TARGET_WIDTH" 2>/dev/null || true
done

# Signal daemon to refresh (pick up new dimensions)
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null)
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
if [ -f "$DAEMON_PID_FILE" ]; then
    kill -USR1 "$(cat "$DAEMON_PID_FILE")" 2>/dev/null || true
fi
