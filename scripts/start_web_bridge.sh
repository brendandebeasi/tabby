#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && cd .. >/dev/null 2>&1 && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
source "$(dirname "${BASH_SOURCE[0]}")/_config_path.sh"
CONFIG_FILE="$TABBY_CONFIG_FILE"

WEB_ENABLED=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "enabled:" | awk '{print $2}' | tr -d '"' || echo "false")
WEB_ENABLED=${WEB_ENABLED:-false}

if [[ "$WEB_ENABLED" != "true" ]]; then
    exit 0
fi

WEB_HOST=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "host:" | awk '{print $2}' | tr -d '"' || echo "127.0.0.1")
WEB_PORT=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "port:" | awk '{print $2}' | tr -d '"' || echo "8080")
WEB_USER=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "auth_user:" | awk '{print $2}' | tr -d '"' || echo "")
WEB_PASS=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "auth_pass:" | awk '{print $2}' | tr -d '"' || echo "")

WEB_HOST=${WEB_HOST:-127.0.0.1}
WEB_PORT=${WEB_PORT:-8080}

if [[ -z "$WEB_USER" || -z "$WEB_PASS" ]]; then
    echo "Tabby Web enabled but auth_user/auth_pass not set" >&2
    exit 0
fi

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
BRIDGE_BIN="$CURRENT_DIR/bin/tabby-web-bridge"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
BRIDGE_PID_FILE="/tmp/tabby-web-bridge-${SESSION_ID}.pid"

if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$BRIDGE_BIN" ]; then
    echo "Tabby Web binaries not found. Run scripts/install.sh" >&2
    exit 1
fi

if [ -f "$DAEMON_PID_FILE" ]; then
    DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
    if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        :
    else
        rm -f "$DAEMON_PID_FILE"
    fi
fi

if [ ! -f "$DAEMON_PID_FILE" ]; then
    "$DAEMON_BIN" -session "$SESSION_ID" >/tmp/tabby-daemon-${SESSION_ID}.log 2>&1 &
fi

if [ -f "$BRIDGE_PID_FILE" ]; then
    BRIDGE_PID=$(cat "$BRIDGE_PID_FILE" 2>/dev/null || echo "")
    if [ -n "$BRIDGE_PID" ] && kill -0 "$BRIDGE_PID" 2>/dev/null; then
        exit 0
    fi
    rm -f "$BRIDGE_PID_FILE"
fi

"$BRIDGE_BIN" -session "$SESSION_ID" -host "$WEB_HOST" -port "$WEB_PORT" -auth-user "$WEB_USER" -auth-pass "$WEB_PASS" >/tmp/tabby-web-bridge-${SESSION_ID}.log 2>&1 &
