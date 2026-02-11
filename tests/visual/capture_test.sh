#!/usr/bin/env bash
set -e

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-visual}"
tmux() { command tmux -L "$TABBY_TEST_SOCKET" -f /dev/null "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"

SCREENSHOT_DIR="tests/screenshots"
mkdir -p "$SCREENSHOT_DIR"/{baseline,current,diffs}

echo "=== Capturing: Horizontal 3 Groups ==="
tmux kill-session -t visual 2>/dev/null || true
tmux new-session -d -s visual
tmux rename-window -t visual:0 "SD|app"
tmux new-window -t visual -n "GP|tool"
tmux new-window -t visual -n "notes"
tmux select-window -t visual:0

tmux set-option -g @tmux_tabs_test 1

tmux run-shell "$PROJECT_ROOT/tabby.tmux"

tmux run-shell -b "$PROJECT_ROOT/bin/render-status > $SCREENSHOT_DIR/current/horizontal-3-groups.txt"

sleep 1

if command -v ansi2html >/dev/null 2>&1; then
	cat "$SCREENSHOT_DIR/current/horizontal-3-groups.txt" | ansi2html > "$SCREENSHOT_DIR/current/horizontal-3-groups.html"
fi

echo "=== Capturing: Sidebar Open ==="
tmux run-shell "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
sleep 1

SIDEBAR_PANE=$(tmux list-panes -t visual -F "#{pane_id}|#{pane_current_command}" | awk -F'|' '$2 ~ /^(sidebar|sidebar-renderer)$/ {print $1}')
	if [ -n "$SIDEBAR_PANE" ]; then
		# Capture the sidebar pane content
		tmux capture-pane -t "$SIDEBAR_PANE" -e -p > "$SCREENSHOT_DIR/current/sidebar-open.txt"
	else
		# Fallback to main pane if sidebar not found
		tmux capture-pane -t visual:0 -e -p > "$SCREENSHOT_DIR/current/sidebar-open.txt"
	fi

if command -v ansi2html >/dev/null 2>&1; then
	cat "$SCREENSHOT_DIR/current/sidebar-open.txt" | ansi2html > "$SCREENSHOT_DIR/current/sidebar-open.html"
fi

tmux kill-session -t visual

echo "Captures saved to $SCREENSHOT_DIR/current/"
