#!/usr/bin/env bash
# set_group_marker.sh - Set a group's marker in tabby config
# Usage: set_group_marker.sh <group_name> <query_or_marker>

set -eu

NAME="${1:-}"
RAW_QUERY="${2:-}"

if [ -z "$NAME" ]; then
    tmux display-message "Error: Usage: set_group_marker.sh <group_name> <query_or_marker>"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"

# Source config path helper
source "$SCRIPT_DIR/_config_path.sh"

QUERY="$(printf '%s' "$RAW_QUERY" | sed 's/^ *//; s/ *$//')"
MARKER=""

if [ -n "$QUERY" ]; then
    if printf '%s' "$QUERY" | LC_ALL=C grep -q '[^ -~]'; then
        MARKER="$QUERY"
    else
        q="$(printf '%s' "$QUERY" | tr '[:upper:]' '[:lower:]')"
        case "$q" in
            *term*|*shell*|*console*) MARKER="💻" ;;
            *code*|*dev*|*program*) MARKER="🧠" ;;
            *folder*|*dir*|*file*) MARKER="📁" ;;
            *git*|*branch*|*repo*) MARKER="🌿" ;;
            *bug*|*fix*) MARKER="🐞" ;;
            *test*|*qa*) MARKER="🧪" ;;
            *db*|*data*|*sql*) MARKER="🗄️" ;;
            *web*|*world*|*globe*) MARKER="🌍" ;;
            *star*|*fav*) MARKER="★" ;;
            *heart*|*love*) MARKER="❤" ;;
            *fire*|*hot*) MARKER="🔥" ;;
            *rocket*|*launch*) MARKER="🚀" ;;
            *bolt*|*lightning*|*fast*) MARKER="⚡" ;;
            *cat*) MARKER="🐱" ;;
            *book*|*doc*) MARKER="📚" ;;
            *music*|*audio*) MARKER="🎵" ;;
            *) MARKER="$QUERY" ;;
        esac
    fi
fi

if ! TABBY_CONFIG="$TABBY_CONFIG_FILE" "$PLUGIN_DIR/bin/manage-group" set-marker "$NAME" "$MARKER" 2>/tmp/tabby-error.log; then
    ERROR=$(cat /tmp/tabby-error.log)
    tmux display-message "Error: $ERROR"
    rm -f /tmp/tabby-error.log
    exit 1
fi
rm -f /tmp/tabby-error.log

DAEMON_PID=$(tmux show-option -gqv @tabby_daemon_pid 2>/dev/null || true)
if [ -z "$DAEMON_PID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
    PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$PID_FILE" ]; then
        DAEMON_PID=$(cat "$PID_FILE" 2>/dev/null || true)
        [ -n "$DAEMON_PID" ] && tmux set-option -g @tabby_daemon_pid "$DAEMON_PID" 2>/dev/null || true
    fi
fi
if [ -n "$DAEMON_PID" ]; then
    kill -USR1 "$DAEMON_PID" 2>/dev/null || true
fi

if [ -n "$MARKER" ]; then
    tmux display-message -d 1500 "Group marker -> $MARKER"
else
    tmux display-message -d 1500 "Group marker cleared"
fi
