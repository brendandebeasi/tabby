#!/bin/bash
# Rename a pane and lock the title
# Usage: rename_pane.sh <pane_id> <new_title>

PANE_ID="$1"
NEW_TITLE="$2"

if [ -z "$PANE_ID" ] || [ -z "$NEW_TITLE" ]; then
    exit 1
fi

# Set the pane title
tmux select-pane -t "$PANE_ID" -T "$NEW_TITLE"

# Lock it with our custom option
tmux set-option -p -t "$PANE_ID" @tabby_pane_title "$NEW_TITLE"
