#!/usr/bin/env bash
# opencode-tabby-hook.sh -- Bridge opencode-notifier events to tabby indicators
EVENT="${1:-}"
INDICATOR="/Users/b/git/tabby/scripts/set-tabby-indicator.sh"

if [ -z "${TMUX_PANE:-}" ] && [ -n "${TMUX:-}" ]; then
    TMUX_PANE=$(tmux display-message -p '#{pane_id}' 2>/dev/null || true)
    export TMUX_PANE
fi
case "$EVENT" in
    start|busy)
        "$INDICATOR" input 0
        "$INDICATOR" busy 1
        ;;
    complete|permission|question|subagent_complete)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        ;;
    error)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        ;;
esac
