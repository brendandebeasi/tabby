#!/bin/bash
# rename_group.sh - Rename a group in the tabby config
# Usage: rename_group.sh <old-name> <new-name>

OLD_NAME="$1"
NEW_NAME="$2"

if [ -z "$OLD_NAME" ] || [ -z "$NEW_NAME" ]; then
    tmux display-message "Error: Usage: rename_group.sh <old-name> <new-name>"
    exit 1
fi

# Get the script directory and plugin root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"

# Rename the group using manage-group CLI
if ! "$PLUGIN_DIR/bin/manage-group" rename "$OLD_NAME" "$NEW_NAME" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

# Signal all sidebars to reload config
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
