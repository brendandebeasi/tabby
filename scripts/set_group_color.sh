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

# Set the group color using manage-group CLI
if ! "$PLUGIN_DIR/bin/manage-group" set-color "$NAME" "$COLOR" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

# Signal all sidebars to reload config
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
