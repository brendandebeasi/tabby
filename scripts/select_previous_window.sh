#!/usr/bin/env bash
# Select the most recently visited window from history when a window is closed.
# Called from window-unlinked hook (before signal_sidebar and refresh_status).

HISTORY=$(tmux show-option -gqv @tabby_window_history 2>/dev/null || echo "")
[ -z "$HISTORY" ] && exit 0

# Get list of existing window IDs
EXISTING=$(tmux list-windows -F '#{window_id}' 2>/dev/null)
[ -z "$EXISTING" ] && exit 0

# Walk the history stack and select the first window that still exists
IFS=',' read -ra ITEMS <<< "$HISTORY"
CLEANED=""
for item in "${ITEMS[@]}"; do
    [ -z "$item" ] && continue
    if echo "$EXISTING" | grep -qF "$item"; then
        # This window still exists
        if [ -z "$CLEANED" ]; then
            # First valid entry - select it
            tmux select-window -t "$item" 2>/dev/null || true
            CLEANED="$item"
        else
            CLEANED="$CLEANED,$item"
        fi
    fi
    # Skip entries for windows that no longer exist (pruned)
done

# Save cleaned history
tmux set-option -g @tabby_window_history "$CLEANED"
exit 0
