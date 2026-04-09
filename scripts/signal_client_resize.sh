#!/usr/bin/env bash
# Called from client-resized / client-attached paths.
# Force every tmux window to adopt the current client geometry immediately,
# then ask the daemon for a single eager sidebar sync/render pass.
set -eu

CLIENT_WIDTH="${1:-}"
CLIENT_HEIGHT="${2:-}"
SESSION_ID="$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")"

if [ -n "$CLIENT_WIDTH" ] && [ -n "$CLIENT_HEIGHT" ]; then
    if printf '%s' "$CLIENT_WIDTH" | grep -Eq '^[0-9]+$' && printf '%s' "$CLIENT_HEIGHT" | grep -Eq '^[0-9]+$'; then
        while IFS= read -r window_id; do
            [ -z "$window_id" ] && continue
            tmux resize-window -x "$CLIENT_WIDTH" -y "$CLIENT_HEIGHT" -t "$window_id" 2>/dev/null || true
        done < <(tmux list-windows -F '#{window_id}' 2>/dev/null || true)
    fi
fi

PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
if [ -n "$SESSION_ID" ] && [ -f "$PID_FILE" ]; then
    kill -USR2 "$(cat "$PID_FILE")" 2>/dev/null || true
fi
