#!/usr/bin/env bash
# Track window selection history for proper focus restoration on close.
# Stores a short stack of recently visited window IDs in a tmux option.
# Called from after-select-window hook.

CURRENT_ID=$(tmux display-message -p '#{window_id}')
[ -z "$CURRENT_ID" ] && exit 0

HISTORY=$(tmux show-option -gqv @tabby_window_history 2>/dev/null || echo "")

# Remove current window from history to avoid duplicates, then prepend it
FILTERED=""
IFS=',' read -ra ITEMS <<< "$HISTORY"
for item in "${ITEMS[@]}"; do
    [ "$item" = "$CURRENT_ID" ] && continue
    [ -z "$item" ] && continue
    if [ -z "$FILTERED" ]; then
        FILTERED="$item"
    else
        FILTERED="$FILTERED,$item"
    fi
done

# Prepend current window and cap at 20 entries
if [ -z "$FILTERED" ]; then
    NEW_HISTORY="$CURRENT_ID"
else
    NEW_HISTORY="$CURRENT_ID,$FILTERED"
fi

# Trim to 20 entries max
NEW_HISTORY=$(echo "$NEW_HISTORY" | cut -d',' -f1-20)

tmux set-option -g @tabby_window_history "$NEW_HISTORY"
exit 0
