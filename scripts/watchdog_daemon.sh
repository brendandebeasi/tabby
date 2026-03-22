#!/usr/bin/env bash
# watchdog_daemon.sh — Restart tabby-daemon on unexpected exit
#
# Usage: watchdog_daemon.sh [daemon args...]
#   e.g. watchdog_daemon.sh -session '$0'
#        watchdog_daemon.sh -session '$0' -debug
#
# The daemon writes a clean-stop sentinel file on intentional shutdown.
# If the sentinel exists after exit, the watchdog exits too.
# If no sentinel (crash, SIGKILL, OOM), the watchdog restarts the daemon.
# On crash, invokes crash-handler.sh for notifications, GitHub issues, and investigation.

set -u

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
CRASH_HOOK="$CURRENT_DIR/scripts/crash-handler.sh"

MAX_RESTARTS=5
RESTART_WINDOW=60   # seconds — reset restart counter after this
RESTART_DELAY=1     # seconds between restart attempts

# Extract session ID from args for sentinel path
SESSION_ID=""
PREV_ARG=""
for arg in "$@"; do
    if [ "$PREV_ARG" = "-session" ]; then
        SESSION_ID="$arg"
    fi
    PREV_ARG="$arg"
done

if [ -z "$SESSION_ID" ]; then
    echo "watchdog: cannot determine session ID from args: $*" >&2
    exec "$DAEMON_BIN" "$@"
fi

SENTINEL="/tmp/tabby-daemon-${SESSION_ID}.clean-stop"
WATCHDOG_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.watchdog.pid"
WATCHDOG_LOG="/tmp/tabby-daemon-${SESSION_ID}-crash.log"

# Write our PID so toggle scripts can kill us
echo $$ > "$WATCHDOG_PID_FILE"

# Clean up watchdog PID file on exit
trap 'rm -f "$WATCHDOG_PID_FILE"' EXIT

RESTART_COUNT=0
WINDOW_START=$(date +%s)

while true; do
    rm -f "$SENTINEL"

    "$DAEMON_BIN" "$@"
    EXIT_CODE=$?

    # Check if clean shutdown was requested (daemon or toggle script wrote sentinel)
    if [ -f "$SENTINEL" ]; then
        rm -f "$SENTINEL"
        exit 0
    fi

    # If another daemon is already running, exit — we lost the race
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        OTHER_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$OTHER_PID" ] && kill -0 "$OTHER_PID" 2>/dev/null; then
            exit 0
        fi
    fi

    NOW=$(date +%s)
    ELAPSED=$((NOW - WINDOW_START))

    # Reset counter if outside the restart window
    if [ "$ELAPSED" -gt "$RESTART_WINDOW" ]; then
        RESTART_COUNT=0
        WINDOW_START=$NOW
    fi

    RESTART_COUNT=$((RESTART_COUNT + 1))

    if [ "$RESTART_COUNT" -gt "$MAX_RESTARTS" ]; then
        printf "%s WATCHDOG_GIVE_UP restarts=%d window=%ds session=%s\n" \
            "$(date '+%Y/%m/%d %H:%M:%S')" "$MAX_RESTARTS" "$RESTART_WINDOW" "$SESSION_ID" \
            >> "$WATCHDOG_LOG" 2>/dev/null || true
        # Give-up path: heavy investigation (GH issue + OpenCode) — wait before exiting
        if [ -x "$CRASH_HOOK" ]; then
            "$CRASH_HOOK" "$SESSION_ID" "$EXIT_CODE" "$RESTART_COUNT" "$MAX_RESTARTS"
        fi
        exit 1
    fi

    printf "%s WATCHDOG_RESTART exit_code=%d attempt=%d/%d session=%s\n" \
        "$(date '+%Y/%m/%d %H:%M:%S')" "$EXIT_CODE" "$RESTART_COUNT" "$MAX_RESTARTS" "$SESSION_ID" \
        >> "$WATCHDOG_LOG" 2>/dev/null || true

    # Transient crash: lightweight notification — runs in background, doesn't block restart
    if [ -x "$CRASH_HOOK" ]; then
        "$CRASH_HOOK" "$SESSION_ID" "$EXIT_CODE" "$RESTART_COUNT" "$MAX_RESTARTS" &
    fi

    sleep "$RESTART_DELAY"
done
