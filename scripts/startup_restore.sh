#!/usr/bin/env bash
set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
LOG_FILE="/tmp/tabby-startup.log"

SESSION_ID="${1:-${TABBY_SESSION_ID:-}}"
if [ "$SESSION_ID" = "#{session_id}" ]; then
    SESSION_ID=""
fi
if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
fi
if [ -z "$SESSION_ID" ]; then
    SESSION_ID=$(tmux list-sessions -F '#{session_id}' 2>/dev/null | head -n 1 || true)
fi

MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
AUTO_START=$(tmux show-option -gqv @tabby_auto_start 2>/dev/null || echo "")
printf "%s startup_restore sid=%s mode=%s auto=%s\n" "$(date '+%Y-%m-%d %H:%M:%S')" "${SESSION_ID:-none}" "${MODE:-unset}" "${AUTO_START:-unset}" >> "$LOG_FILE" 2>/dev/null || true

"$CURRENT_DIR/scripts/restore_sidebar.sh" "$SESSION_ID" >> "$LOG_FILE" 2>&1 || true
