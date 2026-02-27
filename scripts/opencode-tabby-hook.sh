#!/usr/bin/env bash
set -eu

RAW_EVENT="${1:-}"
PROJECT_NAME="${2:-}"
SESSION_TITLE="${3:-}"
NOTIFIER_MESSAGE="${4:-}"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
TABBY_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
INDICATOR="$SCRIPT_DIR/set-tabby-indicator.sh"
LOG_FILE="/tmp/tabby-opencode-hook.log"

# Use tmux from PATH — never hardcode a specific installation path
TMUX_CMD="tmux"

# ── Resolve TMUX_PANE ─────────────────────────────────────────────────────
# The opencode-notifier plugin often spawns hooks without TMUX/TMUX_PANE.
# We MUST find the correct pane — using the active pane is wrong because
# the user may have switched windows since the hook was registered.

if [ -z "${TMUX:-}" ]; then
    # Try to discover tmux socket from running server
    TMUX_SOCKET=$($TMUX_CMD display-message -p '#{socket_path},#{pid},#{client_tty}' 2>/dev/null || true)
    if [ -n "$TMUX_SOCKET" ]; then
        export TMUX="$TMUX_SOCKET"
    fi
fi

# Do NOT use `display-message -p` as fallback — that returns the active pane,
# which may be a completely different window. Only trust TMUX_PANE if it was
# set by tmux itself, or if we find it via process tree walking.

# Walk up from our PID to find a parent that's in a tmux pane
if [ -z "${TMUX_PANE:-}" ]; then
    SEARCH_PID=$$
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        SEARCH_PID=$(ps -o ppid= -p "$SEARCH_PID" 2>/dev/null | tr -d ' ') || break
        [ -z "$SEARCH_PID" ] || [ "$SEARCH_PID" = "1" ] && break
        # Check if this PID matches any tmux pane
        FOUND_PANE=$($TMUX_CMD list-panes -a -F '#{pane_pid}|#{pane_id}' 2>/dev/null | grep "^${SEARCH_PID}|" | head -1 | cut -d'|' -f2)
        if [ -n "$FOUND_PANE" ]; then
            TMUX_PANE="$FOUND_PANE"
            export TMUX_PANE
            break
        fi
    done
fi

# ── Parse event ───────────────────────────────────────────────────────────
# OpenCode events come as either:
#   1. Plain string: "complete", "permission", etc.
#   2. JSON: {"event": "complete", ...} or {"type": "complete", ...}
EVENT="$RAW_EVENT"
if [[ "$EVENT" =~ ^\{.*\}$ ]]; then
    # Try jq first (fast, reliable), fall back to python3
    if command -v jq &>/dev/null; then
        PARSED=$(echo "$EVENT" | jq -r '(.event // .type // .name // empty)' 2>/dev/null || true)
    elif command -v python3 &>/dev/null; then
        PARSED=$(python3 -c "
import json,sys
try:
    data=json.loads(sys.stdin.read())
    for key in ('event','type','name'):
        v=data.get(key)
        if isinstance(v,str) and v:
            print(v)
            break
except Exception:
    pass
" <<< "$EVENT" 2>/dev/null || true)
    else
        PARSED=""
    fi
    if [ -n "$PARSED" ]; then
        EVENT="$PARSED"
    fi
fi

printf "%s event=%s pane=%s\n" "$(date '+%Y-%m-%d %H:%M:%S')" "$EVENT" "${TMUX_PANE:-}" >> "$LOG_FILE"

# ── Get tmux context for notifications ────────────────────────────────────
WINDOW_NAME=""
WINDOW_INDEX=""
PANE_NUM=""
TMUX_TARGET=""
SESSION_NAME=""

if [ -n "${TMUX_PANE:-}" ]; then
    WINDOW_NAME=$($TMUX_CMD display-message -t "$TMUX_PANE" -p '#W' 2>/dev/null || true)
    WINDOW_INDEX=$($TMUX_CMD display-message -t "$TMUX_PANE" -p '#I' 2>/dev/null || true)
    PANE_NUM=$($TMUX_CMD display-message -t "$TMUX_PANE" -p '#P' 2>/dev/null || true)
    SESSION_NAME=$($TMUX_CMD display-message -t "$TMUX_PANE" -p '#{session_name}' 2>/dev/null || true)
    TMUX_TARGET="${SESSION_NAME}:${WINDOW_INDEX}.${PANE_NUM}"
fi


# ── Query OpenCode DB for latest assistant message ─────────────────────────────
OPENCODE_DB="$HOME/.local/share/opencode/opencode.db"
get_last_assistant_text() {
    local max_chars="${1:-200}"
    if ! command -v sqlite3 &>/dev/null || [ ! -f "$OPENCODE_DB" ]; then
        return
    fi
    sqlite3 "$OPENCODE_DB" "
        SELECT substr(json_extract(p.data, '$.text'), 1, $max_chars)
        FROM part p
        JOIN message m ON p.message_id = m.id
        WHERE m.session_id = (SELECT id FROM session ORDER BY time_updated DESC LIMIT 1)
          AND json_extract(m.data, '$.role') = 'assistant'
          AND json_extract(p.data, '$.type') = 'text'
          AND length(json_extract(p.data, '$.text')) > 5
        ORDER BY p.time_created DESC
        LIMIT 1
    " 2>/dev/null
}

# ── Send notification ─────────────────────────────────────────────────────
send_notification() {
    local fallback_message="$1"

    # Only notify if terminal-notifier is installed
    if ! command -v terminal-notifier &>/dev/null; then
        return
    fi

    # Title: "projectName: sessionTitle" or fall back to window name
    local title
    if [ -n "$PROJECT_NAME" ] && [ -n "$SESSION_TITLE" ]; then
        title="${PROJECT_NAME}: ${SESSION_TITLE}"
    elif [ -n "$PROJECT_NAME" ]; then
        title="$PROJECT_NAME"
    else
        title="OpenCode [${WINDOW_NAME:-opencode}]"
    fi

    # Body: last assistant message from DB, or notifier message, or fallback
    # Strip markdown formatting (bold, italic, code, links) for clean notification text
    local db_text
    db_text=$(get_last_assistant_text 200)
    db_text=$(echo "$db_text" | sed -E 's/\*\*([^*]+)\*\*/\1/g; s/\*([^*]+)\*/\1/g; s/`([^`]+)`/\1/g; s/\[([^]]+)\]\([^)]+\)/\1/g; s/^#+\s*//; s/^[-*]\s*//')
    local message="${db_text:-${NOTIFIER_MESSAGE:-$fallback_message}}"

    local args=(
        -title "$title"
        -message "$message"
        -sound default
        -group "opencode-${WINDOW_INDEX:-0}"
    )

    terminal-notifier "${args[@]}" &>/dev/null &
}

# ── Set indicators + notify ───────────────────────────────────────────────
case "$EVENT" in
    start|busy|prompt|working|user_prompt_submit)
        "$INDICATOR" input 0
        "$INDICATOR" busy 1
        ;;
    complete|stop|done)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        send_notification "Task complete"
        ;;
    permission|question)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        send_notification "Needs input"
        ;;
    subagent_complete)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        # No notification for subagent — only notify on final complete
        ;;
    error|failed)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        send_notification "Error occurred"
        ;;
    *)
        # Unknown event — log but don't change state aggressively.
        # Only clear busy if we have a valid pane; otherwise skip entirely.
        if [ -n "${TMUX_PANE:-}" ]; then
            "$INDICATOR" busy 0
            "$INDICATOR" input 1
        fi
        ;;
esac
