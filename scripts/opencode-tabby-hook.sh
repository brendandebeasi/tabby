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


# ── Query OpenCode DB ─────────────────────────────────────────────────────
OPENCODE_DB="$HOME/.local/share/opencode/opencode.db"

# Get the most recently updated session's ID and directory
get_latest_session_info() {
    if ! command -v sqlite3 &>/dev/null || [ ! -f "$OPENCODE_DB" ]; then
        return
    fi
    # Returns: session_id|directory
    sqlite3 "$OPENCODE_DB" "
        SELECT id, directory FROM session ORDER BY time_updated DESC LIMIT 1
    " 2>/dev/null
}

get_last_assistant_text() {
    local session_id="${1:-}"
    local max_chars="${2:-300}"
    if ! command -v sqlite3 &>/dev/null || [ ! -f "$OPENCODE_DB" ]; then
        return
    fi
    local where_clause
    if [ -n "$session_id" ]; then
        where_clause="WHERE m.session_id = '$session_id'"
    else
        where_clause="WHERE m.session_id = (SELECT id FROM session ORDER BY time_updated DESC LIMIT 1)"
    fi
    sqlite3 "$OPENCODE_DB" "
        SELECT substr(json_extract(p.data, '\$.text'), 1, $max_chars)
        FROM part p
        JOIN message m ON p.message_id = m.id
        $where_clause
          AND json_extract(m.data, '\$.role') = 'assistant'
          AND json_extract(p.data, '\$.type') = 'text'
          AND length(json_extract(p.data, '\$.text')) > 5
        ORDER BY p.time_created DESC
        LIMIT 1
    " 2>/dev/null
}

# Strip markdown formatting for clean notification text
strip_markdown() {
    sed -E 's/\*\*([^*]+)\*\*/\1/g; s/\*([^*]+)\*/\1/g; s/`([^`]+)`/\1/g; s/\[([^]]+)\]\([^)]+\)/\1/g; s/^#+\s*//; s/^[-*]\s*//' | head -c 300
}

# ── Send notification ─────────────────────────────────────────────────────
OPENCODE_BUNDLE_ID="ai.opencode.desktop"
EMOJI_RENDERER="$TABBY_DIR/bin/emoji-to-png"
EMOJI_CACHE_DIR="/tmp/tabby-emoji-cache"
TABBY_CONFIG="$HOME/.config/tabby/config.yaml"

# Get the emoji icon for the current window's group from tabby config.
# First tries matching WINDOW_NAME against group patterns (e.g. "SD|foo" matches ^SD\|).
# Falls back to matching PROJECT_NAME against group names (e.g. "StudioDome" matches name).
get_group_emoji() {
    local window_name="${1:-}"
    local project_name="${2:-}"
    [ ! -f "$TABBY_CONFIG" ] && return
    [ -z "$window_name" ] && [ -z "$project_name" ] && return

    # Parse groups from YAML config: extract name, pattern, and icon
    local raw_icon
    raw_icon=$(awk -v wname="$window_name" -v pname="$project_name" '
        /^groups:/ { in_groups=1; next }
        in_groups && /^[^ ]/ { exit }
        in_groups && /- name:/ {
            gsub(/.*- name: */, ""); gsub(/"/, "")
            current_name = $0
        }
        in_groups && /pattern:/ {
            gsub(/.*pattern: */, ""); gsub(/"/, ""); gsub(/^\^/, "")
            current_pattern = $0
        }
        in_groups && /icon:/ {
            gsub(/.*icon: */, ""); gsub(/"/, "")
            icon = $0
            if (current_pattern == ".*") {
                fallback = icon
            } else if (wname != "" && wname ~ current_pattern) {
                print icon; exit
            } else if (pname != "" && tolower(pname) == tolower(current_name)) {
                print icon; exit
            }
        }
        END { if (fallback) print fallback }
    ' "$TABBY_CONFIG" 2>/dev/null)
    [ -z "$raw_icon" ] && return

    # Decode YAML unicode escapes (\U0001F30E → 🌎)
    # macOS system bash (3.2) doesn't support \U in printf '%b',
    # so fall back to python3 if printf didn't actually decode.
    local decoded
    decoded=$(printf '%b' "$raw_icon")
    if [[ "$decoded" == *'\U'* ]]; then
        decoded=$(python3 -c "import sys; sys.stdout.write(\"$raw_icon\")" 2>/dev/null) || decoded="$raw_icon"
    fi
    printf '%s' "$decoded"
}

# Render an emoji to a cached PNG image for notification thumbnails.
# Returns the path to the PNG file.
get_emoji_image() {
    local emoji="${1:-}"
    [ -z "$emoji" ] && return
    [ ! -x "$EMOJI_RENDERER" ] && return

    mkdir -p "$EMOJI_CACHE_DIR"

    # Use a hash of the emoji as filename to handle any unicode
    local hash
    hash=$(printf '%s' "$emoji" | md5 -q 2>/dev/null || printf '%s' "$emoji" | md5sum 2>/dev/null | cut -d' ' -f1)
    local png_path="$EMOJI_CACHE_DIR/${hash}.png"

    # Only render if not already cached
    if [ ! -f "$png_path" ]; then
        "$EMOJI_RENDERER" "$emoji" "$png_path" 256 2>/dev/null || return
    fi

    echo "$png_path"
}

send_notification() {
    local fallback_message="$1"
    local event_type="${2:-complete}"

    # Prefer growlrrr (custom icon support), fall back to terminal-notifier
    local use_growlrrr=false
    if command -v growlrrr &>/dev/null; then
        use_growlrrr=true
    elif ! command -v terminal-notifier &>/dev/null; then
        return
    fi

    # Look up latest session from DB for deep-linking and rich content
    local session_info session_id session_dir
    session_info=$(get_latest_session_info)
    session_id=$(echo "$session_info" | cut -d'|' -f1)
    session_dir=$(echo "$session_info" | cut -d'|' -f2)

    # Title: project name (concise, like a chat app)
    local title
    if [ -n "$PROJECT_NAME" ]; then
        title="$PROJECT_NAME"
    else
        title="OpenCode"
    fi

    # Subtitle: session title (what the agent was working on)
    local subtitle=""
    if [ -n "$SESSION_TITLE" ]; then
        subtitle="$SESSION_TITLE"
    fi

    # Body: last assistant message from DB, stripped of markdown
    local db_text
    db_text=$(get_last_assistant_text "$session_id" 300 | strip_markdown)
    local message="${db_text:-${NOTIFIER_MESSAGE:-$fallback_message}}"

    # Deep-link strategy:
    # 1. If we have tmux context → focus_pane.sh to jump to the correct pane (CLI mode)
    # 2. Otherwise → opencode:// URL scheme to open the desktop app
    local focus_cmd=""
    local open_url=""
    if [ -n "$TMUX_TARGET" ]; then
        focus_cmd="$TABBY_DIR/scripts/focus_pane.sh $TMUX_TARGET"
    elif [ -n "$session_dir" ]; then
        open_url="opencode://open-project?directory=${session_dir}"
    fi

    # Group by session to replace stale notifications (not window index)
    local group_id="opencode-${session_id:-${WINDOW_INDEX:-0}}"

    # Resolve group emoji → cached PNG for notification thumbnail
    local emoji_image=""
    local group_emoji
    group_emoji=$(get_group_emoji "$WINDOW_NAME" "$PROJECT_NAME")
    if [ -n "$group_emoji" ]; then
        emoji_image=$(get_emoji_image "$group_emoji")
    fi

    printf "%s notification: title=%s subtitle=%s group=%s focus=%s open=%s emoji=%s\n" \
        "$(date '+%Y-%m-%d %H:%M:%S')" "$title" "$subtitle" "$group_id" "${focus_cmd:-none}" "${open_url:-none}" "${group_emoji:-none}" >> "$LOG_FILE"

    if $use_growlrrr; then
        local args=(
            send
            --appId OpenCode
            --title "$title"
            --subtitle "$subtitle"
            --threadId "$group_id"
        )

        if [ -n "$focus_cmd" ]; then
            args+=(--execute "$focus_cmd")
        elif [ -n "$open_url" ]; then
            args+=(--open "$open_url")
        fi

        if [ -n "$emoji_image" ] && [ -f "$emoji_image" ]; then
            args+=(--image "$emoji_image")
        fi

        case "$event_type" in
            complete|permission|question)
                args+=(--sound default)
                ;;
        esac

        args+=("$message")
        growlrrr "${args[@]}" &>/dev/null &
    else
        # Fallback: terminal-notifier (no custom icon, but still rich content)
        local args=(
            -title "$title"
            -message "$message"
            -sender "$OPENCODE_BUNDLE_ID"
            -group "$group_id"
        )
        if [ -n "$subtitle" ]; then
            args+=(-subtitle "$subtitle")
        fi
        if [ -n "$focus_cmd" ]; then
            args+=(-execute "$focus_cmd")
        elif [ -n "$open_url" ]; then
            args+=(-open "$open_url")
        else
            args+=(-activate "$OPENCODE_BUNDLE_ID")
        fi
        case "$event_type" in
            complete|permission|question)
                args+=(-sound default)
                ;;
        esac
        terminal-notifier "${args[@]}" &>/dev/null &
    fi
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
        send_notification "Task complete" "complete"
        ;;
    permission|question)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        send_notification "Needs input" "permission"
        ;;
    subagent_complete)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        # No notification for subagent — only notify on final complete
        ;;
    error|failed)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        send_notification "Error occurred" "error"
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
