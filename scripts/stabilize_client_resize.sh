#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

SESSION_ID="${1:-}"
WINDOW_ID="${2:-}"
CLIENT_TTY="${3:-}"
CLIENT_WIDTH="${4:-}"
CLIENT_HEIGHT="${5:-}"

if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")
fi

if [ -z "$WINDOW_ID" ]; then
    WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
fi

if [ -n "$CLIENT_TTY" ] && [ -n "$CLIENT_WIDTH" ] && [ -n "$CLIENT_HEIGHT" ]; then
    tmux set-window-option -g window-size latest 2>/dev/null || true
    tmux refresh-client -t "$CLIENT_TTY" -C "${CLIENT_WIDTH}x${CLIENT_HEIGHT}" 2>/dev/null || true
fi

for delay in 0.05 0.20 0.45 0.90; do
    sleep "$delay"
    "$CURRENT_DIR/scripts/ensure_sidebar.sh" "$SESSION_ID" "$WINDOW_ID" >/dev/null 2>&1 || true
    "$CURRENT_DIR/scripts/resize_sidebar.sh" >/dev/null 2>&1 || true
    tmux refresh-client -S 2>/dev/null || true
done
