#!/bin/bash
# toggle_group_collapse.sh - Toggle a group's collapsed state
# Usage: toggle_group_collapse.sh <group-name> <collapse|expand>

GROUP_NAME="$1"
ACTION="$2"

if [ -z "$GROUP_NAME" ] || [ -z "$ACTION" ]; then
    tmux display-message "Error: Usage: toggle_group_collapse.sh <group-name> <collapse|expand>"
    exit 1
fi

# Get current collapsed groups from tmux session option
CURRENT=$(tmux show-options -v -q @tabby_collapsed_groups 2>/dev/null || echo "[]")

# Parse and update the JSON array
if [ "$ACTION" = "collapse" ]; then
    # Add group to collapsed list if not already there
    NEW=$(echo "$CURRENT" | jq -c --arg g "$GROUP_NAME" 'if . == null then [$g] elif index($g) then . else . + [$g] end')
elif [ "$ACTION" = "expand" ]; then
    # Remove group from collapsed list
    NEW=$(echo "$CURRENT" | jq -c --arg g "$GROUP_NAME" 'if . == null then [] else map(select(. != $g)) end')
else
    tmux display-message "Error: Action must be 'collapse' or 'expand'"
    exit 1
fi

# Save back to tmux session option
if [ "$NEW" = "[]" ] || [ -z "$NEW" ]; then
    tmux set-option -u @tabby_collapsed_groups 2>/dev/null
else
    tmux set-option @tabby_collapsed_groups "$NEW"
fi

# Signal all sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
