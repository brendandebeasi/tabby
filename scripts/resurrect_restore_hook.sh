#!/usr/bin/env bash
#
# resurrect_restore_hook.sh — tmux-resurrect post-restore-all hook
#
# Cleans stale Tabby state and re-initializes the sidebar/tabbar after
# a tmux-resurrect restore. Runs once at the end of the restore cycle.
#
# Usage: Called automatically by tmux-resurrect via:
#   @resurrect-hook-post-restore-all "path/to/this/script"

set -euo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- 1. Kill stale Tabby processes from previous session ---

for proc in tabby-daemon sidebar-renderer pane-header tabbar pane-bar; do
    pkill -f "$proc" 2>/dev/null || true
done

# --- 2. Reset mouse escape sequences on all client TTYs ---
# Stale Bubble Tea processes may have left terminals in mouse-capture mode.

for client_tty in $(tmux list-clients -F "#{client_tty}" 2>/dev/null); do
    [ -w "$client_tty" ] || continue
    printf '\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1006l\033[?1015l' > "$client_tty" 2>/dev/null || true
done

# --- 3. Clean runtime files ---

rm -f /tmp/tabby-daemon-*.pid \
      /tmp/tabby-daemon-*.sock \
      /tmp/tabby-sidebar-*.state \
      /tmp/tabby-daemon-*-events.log \
      /tmp/tabby-daemon-*-input.log \
      /tmp/tabby-ensure-debounce-* 2>/dev/null || true

# --- 4. Kill any zombie Tabby panes that survived restore ---
# The save hook strips these, but belt-and-suspenders.

for pane_info in $(tmux list-panes -a -F "#{pane_current_command}|#{pane_id}" 2>/dev/null); do
    cmd="${pane_info%%|*}"
    pane_id="${pane_info##*|}"
    case "$cmd" in
        sidebar-renderer|sidebar|tabby-daemon|pane-header|tabbar|pane-bar)
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
            ;;
    esac
done

# --- 5. Brief pause for tmux to settle after restore ---

sleep 0.5

# --- 6. Re-initialize Tabby based on saved mode ---
# @tabby_sidebar survives restore (resurrect saves/restores global options).

MODE=$(tmux show-option -gqv @tabby_sidebar 2>/dev/null || echo "disabled")

case "$MODE" in
    enabled|horizontal)
        if [ -x "$CURRENT_DIR/restore_sidebar.sh" ]; then
            "$CURRENT_DIR/restore_sidebar.sh" &
        fi
        ;;
esac
