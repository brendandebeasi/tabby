#!/bin/bash
# new_group.sh - Create a new group in the tabby config
# Usage: new_group.sh <name>

NAME="$1"

if [ -z "$NAME" ]; then
    tmux display-message "Error: No group name provided"
    exit 1
fi

# Get the script directory and plugin root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"

# Add the group using manage-group CLI
if ! "$PLUGIN_DIR/bin/manage-group" add "$NAME" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

# Signal all sidebars to reload config (no message needed - group appears in sidebar)
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
