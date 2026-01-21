#!/usr/bin/env bash
# Preserve window name if it has a group prefix (contains "|")
# This prevents automatic-rename from breaking group assignments after split

WINDOW_NAME=$(tmux display-message -p '#{window_name}')

# If window name contains "|", it's in a group - lock the name
if [[ "$WINDOW_NAME" == *"|"* ]]; then
    tmux set-window-option automatic-rename off
fi
