#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

WINDOW_ID="${2:-}"
CLIENT_WIDTH="${4:-}"
CLIENT_HEIGHT="${5:-}"
SESSION_ID="$(tmux display-message -p '#{session_id}' 2>/dev/null || echo "")"

if [ -z "$WINDOW_ID" ]; then
    WINDOW_ID=$(tmux display-message -p '#{window_id}' 2>/dev/null || echo "")
fi

"$CURRENT_DIR/scripts/ensure_sidebar.sh" "$SESSION_ID" "$WINDOW_ID" >/dev/null 2>&1 || true
"$CURRENT_DIR/scripts/signal_client_resize.sh" "$CLIENT_WIDTH" "$CLIENT_HEIGHT" >/dev/null 2>&1 || true
tmux refresh-client -S 2>/dev/null || true
