#!/bin/bash
# delete_group.sh - Delete a group from the tabby config
# Usage: delete_group.sh <name>

NAME="$1"

if [ -z "$NAME" ]; then
    tmux display-message "Error: No group name provided"
    exit 1
fi

# Get the script directory and plugin root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"

# Delete the group using manage-group CLI
if ! "$PLUGIN_DIR/bin/manage-group" delete "$NAME" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

# Signal daemon to reload config immediately
DAEMON_PID=$(tmux show-option -gqv @tabby_daemon_pid 2>/dev/null)
if [ -n "$DAEMON_PID" ]; then
    kill -USR1 "$DAEMON_PID" 2>/dev/null || true
fi
