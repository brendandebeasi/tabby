#!/bin/bash
# set-tabby-indicator.sh - Set tabby indicators on a tmux window
# Usage: set-tabby-indicator.sh [busy|bell|activity|silence] [0|1]
#
# For busy=1 (UserPromptSubmit): Uses the currently focused pane since
# that's where the user just typed their message.
#
# For busy=0/bell=1 (Stop): Uses state files to track which windows
# were marked busy, since focus may have changed.
#
# Indicators:
#   busy     - Animated spinner (for long-running tasks)
#   bell     - Alert icon (task completed, needs attention)
#   activity - Activity marker (unseen output)
#   silence  - Silence marker (no output for a period)

INDICATOR="$1"
VALUE="$2"

# State directory for tracking which windows were marked busy
STATE_DIR="/tmp/tabby-state"
mkdir -p "$STATE_DIR" 2>/dev/null

SESSION=$(tmux display-message -p '#{session_name}' 2>/dev/null)

# Get the window for this Claude session using TMUX_PANE
# TMUX_PANE is inherited from Claude Code and identifies the correct pane
if [ -n "$TMUX_PANE" ]; then
    # Use the pane ID to get its window (works regardless of which window is "active")
    CLAUDE_WIN=$(tmux display-message -t "$TMUX_PANE" -p '#{window_index}' 2>/dev/null)
else
    # Fallback to active window if TMUX_PANE not set
    CLAUDE_WIN=$(tmux display-message -p '#{window_index}' 2>/dev/null)
fi

# Debug logging
echo "=== $(date) ===" >> /tmp/tabby-indicator-debug.log
echo "INDICATOR=$INDICATOR VALUE=$VALUE" >> /tmp/tabby-indicator-debug.log
echo "TMUX_PANE=$TMUX_PANE -> CLAUDE_WIN=$CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
echo "Active window: $(tmux display-message -p '#{window_index}' 2>/dev/null)" >> /tmp/tabby-indicator-debug.log

case "$INDICATOR" in
    busy)
        if [ "$VALUE" = "1" ]; then
            # Mark Claude's window as busy (derived from TMUX_PANE)
            if [ -n "$CLAUDE_WIN" ]; then
                touch "$STATE_DIR/busy-${SESSION}-${CLAUDE_WIN}"
                tmux set-option -t ":$CLAUDE_WIN" -w @tabby_busy 1 2>/dev/null
                echo "Set busy on window $CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
            fi
        else
            # Clear busy ONLY on this Claude's window (not other windows)
            if [ -n "$CLAUDE_WIN" ]; then
                tmux set-option -t ":$CLAUDE_WIN" -wu @tabby_busy 2>/dev/null
                echo "Cleared busy on window $CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
            fi
        fi
        ;;
    bell)
        if [ "$VALUE" = "1" ]; then
            # Set bell ONLY on this Claude's window and clean up its state file
            if [ -n "$CLAUDE_WIN" ]; then
                STATE_FILE="$STATE_DIR/busy-${SESSION}-${CLAUDE_WIN}"
                if [ -f "$STATE_FILE" ]; then
                    tmux set-option -t ":$CLAUDE_WIN" -w @tabby_bell 1 2>/dev/null
                    rm -f "$STATE_FILE"
                    echo "Set bell on window $CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
                fi
            fi
        else
            # Clear bell on focused window (user is now interacting with it)
            if [ -n "$CLAUDE_WIN" ]; then
                tmux set-option -t ":$CLAUDE_WIN" -wu @tabby_bell 2>/dev/null
                echo "Cleared bell on window $CLAUDE_WIN (focused)" >> /tmp/tabby-indicator-debug.log
            fi
        fi
        ;;
    activity)
        if [ "$VALUE" = "1" ]; then
            [ -n "$CLAUDE_WIN" ] && tmux set-option -t ":$CLAUDE_WIN" -w @tabby_activity 1 2>/dev/null
        else
            [ -n "$CLAUDE_WIN" ] && tmux set-option -t ":$CLAUDE_WIN" -wu @tabby_activity 2>/dev/null
        fi
        ;;
    silence)
        if [ "$VALUE" = "1" ]; then
            [ -n "$CLAUDE_WIN" ] && tmux set-option -t ":$CLAUDE_WIN" -w @tabby_silence 1 2>/dev/null
        else
            [ -n "$CLAUDE_WIN" ] && tmux set-option -t ":$CLAUDE_WIN" -wu @tabby_silence 2>/dev/null
        fi
        ;;
esac

# Signal all sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
