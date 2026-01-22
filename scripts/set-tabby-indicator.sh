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

# Get the window for this Claude session
# Strategy: Use TMUX_PANE if valid, otherwise try to find our parent's pane
CLAUDE_WIN=""

# First, verify TMUX_PANE points to an existing pane
if [ -n "$TMUX_PANE" ]; then
    # Check if this pane still exists
    if tmux display-message -t "$TMUX_PANE" -p '#{pane_id}' &>/dev/null; then
        CLAUDE_WIN=$(tmux display-message -t "$TMUX_PANE" -p '#{window_index}' 2>/dev/null)
    fi
fi

# Fallback: try to find pane by walking up process tree to find tmux client
if [ -z "$CLAUDE_WIN" ]; then
    # Get our parent PID chain and find which pane we're in
    CURRENT_PID=$$
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        # Check if this PID is a tmux pane's shell
        FOUND_PANE=$(tmux list-panes -a -F '#{pane_pid}:#{window_index}' 2>/dev/null | grep "^${CURRENT_PID}:" | cut -d: -f2)
        if [ -n "$FOUND_PANE" ]; then
            CLAUDE_WIN="$FOUND_PANE"
            break
        fi
        # Move to parent
        CURRENT_PID=$(ps -o ppid= -p "$CURRENT_PID" 2>/dev/null | tr -d ' ')
        [ -z "$CURRENT_PID" ] && break
    done
fi

# Final fallback: use active window
if [ -z "$CLAUDE_WIN" ]; then
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
    input)
        if [ "$VALUE" = "1" ]; then
            # Set input needed indicator
            if [ -n "$CLAUDE_WIN" ]; then
                tmux set-option -t ":$CLAUDE_WIN" -w @tabby_input 1 2>/dev/null
                echo "Set input on window $CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
            fi
        else
            # Clear input indicator
            if [ -n "$CLAUDE_WIN" ]; then
                tmux set-option -t ":$CLAUDE_WIN" -wu @tabby_input 2>/dev/null
                echo "Cleared input on window $CLAUDE_WIN" >> /tmp/tabby-indicator-debug.log
            fi
        fi
        ;;
esac

# Signal all sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
