#!/bin/bash
# set_group_color.sh - Set a group's color in the tabby config
# Usage: set_group_color.sh <name> <color>

NAME="$1"
COLOR="$2"

if [ -z "$NAME" ] || [ -z "$COLOR" ]; then
    tmux display-message "Error: Usage: set_group_color.sh <name> <color>"
    exit 1
fi

# Get the script directory and plugin root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"
source "$SCRIPT_DIR/_config_path.sh"
TABBY_CONFIG_DIR="$(dirname "$TABBY_CONFIG_FILE")"

# Set the group color using manage-group CLI
if ! TABBY_CONFIG_DIR="$TABBY_CONFIG_DIR" "$PLUGIN_DIR/bin/manage-group" set-color "$NAME" "$COLOR" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

DAEMON_PID=$(tmux show-option -gqv @tabby_daemon_pid 2>/dev/null || true)
if [ -z "$DAEMON_PID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
    PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$PID_FILE" ]; then
        DAEMON_PID=$(cat "$PID_FILE" 2>/dev/null || true)
        [ -n "$DAEMON_PID" ] && tmux set-option -g @tabby_daemon_pid "$DAEMON_PID" 2>/dev/null || true
    fi
fi

if [ -n "$DAEMON_PID" ]; then
    kill -USR1 "$DAEMON_PID" 2>/dev/null || true
fi
