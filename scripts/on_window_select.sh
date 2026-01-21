#!/usr/bin/env bash
# Minimal handler for window selection - just signal sidebar

PID_FILE="/tmp/tmux-tabs-sidebar-$(tmux display-message -p '#{session_id}').pid"
[ -f "$PID_FILE" ] && kill -USR1 "$(cat "$PID_FILE")" 2>/dev/null || true
