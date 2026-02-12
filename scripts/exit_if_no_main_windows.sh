#!/usr/bin/env bash

set -eu

SESSION_ID=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
[ -z "$SESSION_ID" ] && exit 0

MAIN_PANES=0
while IFS= read -r line; do
	[ -z "$line" ] && continue
	cmd=$(printf '%s' "$line" | cut -d'|' -f1)
	start=$(printf '%s' "$line" | cut -d'|' -f2)
	if printf '%s|%s' "$cmd" "$start" | grep -qE '(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header|tabby-daemon)'; then
		continue
	fi
	MAIN_PANES=$((MAIN_PANES + 1))
	break
done < <(tmux list-panes -a -t "$SESSION_ID" -F '#{pane_current_command}|#{pane_start_command}' 2>/dev/null || true)

if [ "$MAIN_PANES" -eq 0 ]; then
	tmux kill-session -t "$SESSION_ID" 2>/dev/null || true
fi
