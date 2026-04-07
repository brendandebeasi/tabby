#!/usr/bin/env bash
# Ensure sidebar exists in current window when that mode is enabled
# Called by tmux hooks when windows are created/switched
#
# Architecture: 1 daemon per session + 1 renderer per window

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

SPAWNING=$(tmux show-option -gqv @tabby_spawning 2>/dev/null || echo "")
if [ "$SPAWNING" = "1" ]; then
    exit 0
fi

# Always query tmux directly for session ID. Passing #{session_id} through
# tmux run-shell embeds e.g. "$0" in the shell command, which sh then
# re-expands to the shell executable name instead of the tmux session ID.
SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")

# On first tmux startup (cold boot), the async run-shell -b in tabby.tmux
# may fire before tmux is fully initialized. Retry a few times with backoff
# rather than bailing — the session-created hook won't fire for the initial
# session (it was created before the plugin loaded), so this is our only
# chance to bootstrap the sidebar on first launch.
if [ -z "$SESSION_ID" ]; then
    for _retry in 1 2 3 4 5; do
        sleep 0.2
        SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")
        [ -n "$SESSION_ID" ] && break
    done
    if [ -z "$SESSION_ID" ]; then
        exit 0
    fi
fi

WINDOW_ID="${2:-}"

if [ -z "$WINDOW_ID" ]; then
    WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
fi
if [ -z "$WINDOW_ID" ] && [ -n "$SESSION_ID" ]; then
    WINDOW_ID=$(tmux list-windows -t "$SESSION_ID" -F "#{window_id}" 2>/dev/null | head -1)
fi

# Debounce - skip if called within 1s to prevent redundant daemon checks
DEBOUNCE_FILE="/tmp/tabby-ensure-debounce-${SESSION_ID:-default}-${WINDOW_ID:-current}.ts"
DEBOUNCE_S=1

if [ -f "$DEBOUNCE_FILE" ]; then
    LAST_RUN=$(cat "$DEBOUNCE_FILE" 2>/dev/null || echo "0")
    NOW=$(date +%s)
    DIFF=$((NOW - LAST_RUN))
    if [ "$DIFF" -lt "$DEBOUNCE_S" ]; then
        exit 0
    fi
fi
date +%s > "$DEBOUNCE_FILE"
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
                if [ -z "$WD_PID" ] || ! kill -0 "$WD_PID" 2>/dev/null; then
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
            for _ in $(seq 1 20); do
                [ -S "$DAEMON_SOCK" ] && break
                sleep 0.1
            done
            # Clear spawning guard - renderers should spawn soon
            tmux set-option -gu @tabby_spawning
        fi
    fi
fi
