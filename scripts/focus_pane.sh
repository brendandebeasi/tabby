#!/usr/bin/env bash
# Focus a specific tmux session/window/pane and bring terminal to foreground
# Usage: focus_pane.sh [session:]window[.pane]
# Examples:
#   focus_pane.sh 2           # Window 2 in default session
#   focus_pane.sh 2.1         # Window 2, pane 1
#   focus_pane.sh main:2.1    # Session "main", window 2, pane 1

TARGET="${1:-0}"

# Debug logging
LOG="/tmp/focus_pane.log"
echo "$(date): focus_pane.sh called with TARGET=$TARGET" >> "$LOG"

# Use full path to tmux (needed when called from terminal-notifier)
TMUX_CMD="/opt/homebrew/bin/tmux"

# Find config file
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/../config.yaml"

# Read terminal app from config
TERMINAL_APP=$(grep "^terminal_app:" "$CONFIG_FILE" 2>/dev/null | awk '{print $2}' || echo "")

echo "  TERMINAL_APP from config: '$TERMINAL_APP'" >> "$LOG"
if [ -z "$TERMINAL_APP" ]; then
    echo "Error: terminal_app not configured in config.yaml" >> "$LOG"
    echo "Error: terminal_app not configured in config.yaml"
    echo "Add: terminal_app: Ghostty  (or iTerm, Terminal, Alacritty, kitty, WezTerm)"
    exit 1
fi

# Parse target: [session:]window[.pane]
if [[ "$TARGET" == *":"* ]]; then
    SESSION="${TARGET%%:*}"
    REST="${TARGET#*:}"
else
    # No session specified - get the first/default session
    SESSION=$($TMUX_CMD list-sessions -F '#{session_name}' 2>/dev/null | head -1)
    REST="$TARGET"
fi

if [[ "$REST" == *"."* ]]; then
    WINDOW="${REST%%.*}"
    PANE="${REST#*.}"
else
    WINDOW="$REST"
    PANE="0"
fi

# Validate session exists
echo "  Checking session: $SESSION" >> "$LOG"
if ! $TMUX_CMD has-session -t "$SESSION" 2>>"$LOG"; then
    echo "  Session '$SESSION' not found - exiting" >> "$LOG"
    echo "Session '$SESSION' not found"
    exit 1
fi
echo "  Session exists" >> "$LOG"

# Select the window and pane in tmux (works because tmux is client-server)
echo "  Parsed: SESSION=$SESSION WINDOW=$WINDOW PANE=$PANE" >> "$LOG"

# Select the window and pane - all clients on this session will see the change
WINDOW_TARGET="${SESSION}:${WINDOW}"
PANE_TARGET="${SESSION}:${WINDOW}.${PANE}"

echo "  Selecting window: $WINDOW_TARGET" >> "$LOG"
$TMUX_CMD select-window -t "$WINDOW_TARGET" 2>>"$LOG" || echo "  select-window failed" >> "$LOG"

echo "  Selecting pane: $PANE_TARGET" >> "$LOG"
$TMUX_CMD select-pane -t "$PANE_TARGET" 2>>"$LOG" || echo "  select-pane failed" >> "$LOG"

# Small delay to ensure tmux has processed the window/pane switch
sleep 0.1

# Signal sidebar to refresh (so tab bar shows correct active tab)
# Get session ID from the target session, not current client
SESSION_ID=$($TMUX_CMD display-message -t "$SESSION" -p '#{session_id}' 2>/dev/null)
echo "  Session ID for refresh: $SESSION_ID" >> "$LOG"
PID_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    SIDEBAR_PID=$(cat "$PID_FILE")
    if [ -n "$SIDEBAR_PID" ] && kill -0 "$SIDEBAR_PID" 2>/dev/null; then
        echo "  Signaling sidebar (PID $SIDEBAR_PID)" >> "$LOG"
        kill -USR1 "$SIDEBAR_PID" 2>/dev/null || true
    fi
else
    echo "  No sidebar PID file at $PID_FILE" >> "$LOG"
fi

# Also refresh the status bar (for horizontal mode)
$TMUX_CMD refresh-client -t "$SESSION" -S 2>/dev/null || true
echo "  Refreshed tmux client status bar" >> "$LOG"

# Bring terminal to foreground using AppleScript
echo "  Running: osascript to activate $TERMINAL_APP" >> "$LOG"
osascript -e "tell application \"$TERMINAL_APP\" to activate" 2>>"$LOG" || echo "  osascript failed" >> "$LOG"
echo "  Done" >> "$LOG"
