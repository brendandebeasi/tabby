#!/usr/bin/env bash
set -eu

if [ "$(tmux show-option -gqv @tabby_spawning 2>/dev/null)" = "1" ]; then
    exit 0
fi

SESSION_ID="${1:-}"
if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")
fi

MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ]; then
    MODE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")
fi

HAS_TABBY_PANES="no"
if [ -n "$SESSION_ID" ]; then
    if tmux list-panes -s -t "$SESSION_ID" -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -qE "(sidebar-renderer|sidebar|tabbar|pane-bar|pane-header)"; then
        HAS_TABBY_PANES="yes"
    fi
fi

if [ "$MODE" = "disabled" ]; then
    tmux set-option -g status on
elif [ "$MODE" = "enabled" ] || [ "$MODE" = "horizontal" ]; then
    tmux set-option -g status off
elif [ "$HAS_TABBY_PANES" = "yes" ]; then
    tmux set-option -g status off
else
    tmux set-option -g status on
fi
