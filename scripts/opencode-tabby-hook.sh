#!/usr/bin/env bash
# opencode-tabby-hook.sh -- Bridge opencode-notifier events to tabby indicators
EVENT="${1:-}"
INDICATOR="/Users/b/git/tabby/scripts/set-tabby-indicator.sh"
case "$EVENT" in
    complete|permission|question|subagent_complete)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        ;;
    error)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        ;;
esac
