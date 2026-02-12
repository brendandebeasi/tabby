#!/usr/bin/env bash
# Show whether the running daemon matches the latest built binary.

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"

resolve_session() {
    local target="${1:-}"

    if [ -n "$target" ]; then
        if [[ "$target" == \$* ]]; then
            SESSION_ID="$target"
            SESSION_NAME="$(tmux display-message -p -t "$SESSION_ID" '#{session_name}' 2>/dev/null || true)"
        else
            SESSION_NAME="$target"
            SESSION_ID="$(tmux display-message -p -t "$SESSION_NAME" '#{session_id}' 2>/dev/null || true)"
        fi
        return
    fi

    SESSION_ID="$(tmux display-message -p '#{session_id}' 2>/dev/null || true)"
    SESSION_NAME="$(tmux display-message -p '#{session_name}' 2>/dev/null || true)"

    if [ -z "$SESSION_ID" ]; then
        local first
        first="$(tmux list-sessions -F '#{session_id} #{session_name}' 2>/dev/null | head -n 1 || true)"
        SESSION_ID="${first%% *}"
        SESSION_NAME="${first#* }"
    fi
}

if ! command -v tmux >/dev/null 2>&1; then
    echo "tmux is required"
    exit 1
fi

if [ ! -f "$DAEMON_BIN" ]; then
    echo "missing daemon binary: $DAEMON_BIN"
    echo "build first: ./scripts/install.sh"
    exit 1
fi

resolve_session "${1:-}"

if [ -z "${SESSION_ID:-}" ] || [ "$SESSION_ID" = "" ]; then
    echo "no tmux session found"
    exit 1
fi

if [ -z "${SESSION_NAME:-}" ] || [ "$SESSION_NAME" = "$SESSION_ID" ]; then
    SESSION_NAME="$(tmux display-message -p -t "$SESSION_ID" '#{session_name}' 2>/dev/null || true)"
fi

PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
SOCK_FILE="/tmp/tabby-daemon-${SESSION_ID}.sock"

BIN_MTIME_EPOCH="$(stat -f '%m' "$DAEMON_BIN")"
BIN_MTIME_HUMAN="$(stat -f '%Sm' -t '%Y-%m-%d %H:%M:%S' "$DAEMON_BIN")"

echo "Tabby Runtime Status"
echo "session: ${SESSION_NAME:-unknown} (${SESSION_ID})"
echo "binary:  $DAEMON_BIN"
echo "built:   $BIN_MTIME_HUMAN"

if [ ! -f "$PID_FILE" ]; then
    echo "daemon:  stopped (pid file missing: $PID_FILE)"
    echo "status:  STALE"
    echo "fix:     TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh && TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh"
    exit 1
fi

DAEMON_PID="$(cat "$PID_FILE" 2>/dev/null || true)"
if [ -z "$DAEMON_PID" ] || ! ps -p "$DAEMON_PID" >/dev/null 2>&1; then
    echo "daemon:  stopped (stale pid file: $PID_FILE)"
    echo "status:  STALE"
    echo "fix:     TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh && TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh"
    exit 1
fi

RUNNING_CMD="$(ps -p "$DAEMON_PID" -o command= 2>/dev/null || true)"
PID_MTIME_EPOCH="$(stat -f '%m' "$PID_FILE")"
PID_MTIME_HUMAN="$(stat -f '%Sm' -t '%Y-%m-%d %H:%M:%S' "$PID_FILE")"

SOCK_STATUS="no"
if [ -S "$SOCK_FILE" ]; then
    SOCK_STATUS="yes"
fi

echo "daemon:  running pid=$DAEMON_PID"
echo "started: $PID_MTIME_HUMAN (pid file)"
echo "socket:  $SOCK_STATUS ($SOCK_FILE)"
echo "cmd:     $RUNNING_CMD"

FRESH="yes"
if [ "$PID_MTIME_EPOCH" -lt "$BIN_MTIME_EPOCH" ]; then
    FRESH="no"
fi

EXPECTED="$DAEMON_BIN -session $SESSION_ID"
if [[ "$RUNNING_CMD" != *"$EXPECTED"* ]]; then
    FRESH="no"
fi

if [ "$FRESH" = "yes" ]; then
    echo "status:  FRESH (running latest build)"
    exit 0
fi

echo "status:  STALE (daemon older than current build or mismatched binary)"
echo "fix:     TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh && TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='${SESSION_NAME:-$SESSION_ID}' ./scripts/toggle_sidebar.sh"
exit 1
