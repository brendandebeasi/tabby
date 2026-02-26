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
source "$SCRIPT_DIR/_config_path.sh"
CONFIG_FILE="$TABBY_CONFIG_FILE"

# Read terminal app from config
TERMINAL_APP=$(grep -E '^\s*terminal_app:' "$CONFIG_FILE" 2>/dev/null | head -1 | sed 's/.*terminal_app:\s*//' | tr -d '"'"'"' ' || echo "")

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

# Signal all sidebars in the session to refresh
# Use tmux list-panes to find sidebar processes directly (more reliable than PID files)
echo "  Signaling all sidebars in session $SESSION" >> "$LOG"
SIGNALED=0
while IFS='|' read -r cmd pid; do
    if [ "$cmd" = "sidebar" ] && [ -n "$pid" ]; then
        echo "    Sending SIGUSR1 to sidebar PID $pid" >> "$LOG"
        kill -USR1 "$pid" 2>/dev/null && ((SIGNALED++)) || true
    fi
done < <($TMUX_CMD list-panes -s -t "$SESSION" -F '#{pane_current_command}|#{pane_pid}' 2>/dev/null)
echo "  Signaled $SIGNALED sidebar(s)" >> "$LOG"

# Also refresh the status bar (for horizontal mode)
$TMUX_CMD refresh-client -t "$SESSION" -S 2>/dev/null || true
echo "  Refreshed tmux client status bar" >> "$LOG"

# Bring terminal to foreground using AppleScript
# For Ghostty: try to raise the specific window containing our tmux session
echo "  Activating $TERMINAL_APP (raising correct window)" >> "$LOG"
if [ "$TERMINAL_APP" = "Ghostty" ]; then
    # Ghostty doesn't expose AppleScript windows directly, but System Events can
    # enumerate and raise windows by title. tmux set-titles puts session info in the title.
    # After select-window, the title updates to reflect the new active window.
    osascript 2>>"$LOG" <<APPLESCRIPT
tell application "Ghostty" to activate
delay 0.05
tell application "System Events"
    tell process "Ghostty"
        set wCount to count of windows
        repeat with i from 1 to wCount
            set wName to name of window i
            if wName contains "tmux" then
                perform action "AXRaise" of window i
                exit repeat
            end if
        end repeat
    end tell
end tell
APPLESCRIPT
    [ $? -ne 0 ] && echo "  Ghostty AXRaise failed, fell back to activate" >> "$LOG"
else
    osascript -e "tell application \"$TERMINAL_APP\" to activate" 2>>"$LOG" || echo "  osascript failed" >> "$LOG"
fi
echo "  Done" >> "$LOG"
