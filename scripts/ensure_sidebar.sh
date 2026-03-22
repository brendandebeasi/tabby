#!/usr/bin/env bash
# Ensure sidebar exists in current window when that mode is enabled
# Called by tmux hooks when windows are created/switched
#
# Architecture: 1 daemon per session + 1 renderer per window

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

LOG="/tmp/tabby-focus.log"
TS=$(date +%s 2>/dev/null || echo "")
SPAWNING=$(tmux show-option -gqv @tabby_spawning 2>/dev/null || echo "")
printf "%s ensure_sidebar start win=%s pane=%s spawning=%s\n" "$TS" "$(tmux display-message -p '#{window_id}' 2>/dev/null || echo '')" "$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo '')" "$SPAWNING" >> "$LOG"
if [ "$SPAWNING" = "1" ]; then
    exit 0
fi

# Always query tmux directly for session ID. Passing #{session_id} through
# tmux run-shell embeds e.g. "$0" in the shell command, which sh then
# re-expands to the shell executable name instead of the tmux session ID.
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")

# Bail out if we can't determine the session — this happens during very early
# startup (tabby.tmux run-shell -b) before tmux is fully initialized.
# Hooks will call us again once the session is ready.
if [ -z "$SESSION_ID" ]; then
    printf "%s ensure_sidebar bail: empty session_id\n" "$TS" >> "$LOG"
    exit 0
fi

WINDOW_ID="${2:-}"

if [ -z "$WINDOW_ID" ]; then
    WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
fi
if [ -z "$WINDOW_ID" ] && [ -n "$SESSION_ID" ]; then
    WINDOW_ID=$(tmux list-windows -t "$SESSION_ID" -F "#{window_id}" 2>/dev/null | head -1)
fi

# Debounce - skip if called within 100ms to prevent flicker
DEBOUNCE_FILE="/tmp/tabby-ensure-debounce-${SESSION_ID:-default}-${WINDOW_ID:-current}.ts"
DEBOUNCE_MS=100

if [ -f "$DEBOUNCE_FILE" ]; then
    LAST_RUN=$(cat "$DEBOUNCE_FILE" 2>/dev/null || echo "0")
    NOW=$(perl -MTime::HiRes=time -e 'printf "%.0f\n", time * 1000' 2>/dev/null || date +%s000)
    DIFF=$((NOW - LAST_RUN))
    if [ "$DIFF" -lt "$DEBOUNCE_MS" ]; then
        exit 0
    fi
fi
perl -MTime::HiRes=time -e 'printf "%.0f\n", time * 1000' 2>/dev/null > "$DEBOUNCE_FILE" || date +%s000 > "$DEBOUNCE_FILE"
SIDEBAR_STATE_FILE="/tmp/tabby-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"

# Get saved sidebar width or default
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width)
if [ -z "$SIDEBAR_WIDTH" ]; then SIDEBAR_WIDTH=25; fi

# Get sidebar position and mode
SIDEBAR_POSITION=$(tmux show-option -gqv @tabby_sidebar_position)
if [ -z "$SIDEBAR_POSITION" ]; then SIDEBAR_POSITION="left"; fi

SIDEBAR_MODE=$(tmux show-option -gqv @tabby_sidebar_mode)
if [ -z "$SIDEBAR_MODE" ]; then SIDEBAR_MODE="full"; fi
TABBAR_HEIGHT=2

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"
WATCHDOG_SCRIPT="$CURRENT_DIR/scripts/watchdog_daemon.sh"

# Get mode: prefer global option (source of truth), fall back to session then state file
MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ]; then
    MODE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")
fi
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
	tmux set-option -g status off

    # Check if CURRENT window has a sidebar-renderer (daemon-based)
    # Check both current command and start command (for reliability during startup)
    HAS_SIDEBAR=$(tmux list-panes -t "${WINDOW_ID:-}" -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -qE "(sidebar-renderer|sidebar)" && echo "yes" || echo "no")

    if [ "$HAS_SIDEBAR" = "no" ]; then
        # Verify daemon is running - it handles spawning renderers
        DAEMON_RUNNING=false
        if [ -S "$DAEMON_SOCK" ]; then
            if [ -f "$DAEMON_PID_FILE" ]; then
                DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
                if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
                    DAEMON_RUNNING=true
                fi
            fi
        fi

        # Start daemon if needed - it will spawn renderers via its ticker loop
        if [ "$DAEMON_RUNNING" = "false" ]; then
            # Check if a watchdog is already running (race with toggle_sidebar_daemon)
            WATCHDOG_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.watchdog.pid"
            if [ -f "$WATCHDOG_PID_FILE" ]; then
                WD_PID=$(cat "$WATCHDOG_PID_FILE" 2>/dev/null || echo "")
                if [ -n "$WD_PID" ] && kill -0 "$WD_PID" 2>/dev/null; then
                    # Watchdog is alive — it will restart daemon, nothing to do
                    printf "%s ensure_sidebar skip: watchdog running pid=%s\n" "$TS" "$WD_PID" >> "$LOG"
                else
                    rm -f "$WATCHDOG_PID_FILE"
                fi
            fi

            if [ ! -f "$WATCHDOG_PID_FILE" ] || ! kill -0 "$(cat "$WATCHDOG_PID_FILE" 2>/dev/null)" 2>/dev/null; then
                rm -f "$DAEMON_SOCK" "$DAEMON_PID_FILE"
                if [ "${TABBY_DEBUG:-}" = "1" ]; then
                    "$WATCHDOG_SCRIPT" -session "$SESSION_ID" -debug &
                else
                    "$WATCHDOG_SCRIPT" -session "$SESSION_ID" &
                fi
            fi
            # Wait for socket
            for i in $(seq 1 20); do
                [ -S "$DAEMON_SOCK" ] && break
                sleep 0.1
            done
        fi

        # Signal daemon for immediate renderer spawning (don't wait for 2s ticker)
        if [ -f "$DAEMON_PID_FILE" ]; then
            kill -USR1 "$(cat "$DAEMON_PID_FILE")" 2>/dev/null || true
        fi
    fi
fi
