#!/usr/bin/env bash
# Kill multiple windows by index
# Usage: kill_windows.sh <index1> [index2] [index3] ...

if [ $# -eq 0 ]; then
    echo "Usage: kill_windows.sh <index1> [index2] [index3] ..." >&2
    exit 1
fi

# Kill windows in reverse order (highest index first to avoid reindexing issues)
for idx in $(echo "$@" | tr ' ' '\n' | sort -rn); do
    [ -n "$idx" ] && tmux kill-window -t ":$idx" 2>/dev/null || true
done

# Switch to a remaining window
tmux select-window -t :0 2>/dev/null || true

# Focus main pane (not sidebar)
main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar' | head -1 | cut -d: -f1)
[ -n "$main_pane" ] && tmux select-pane -t "$main_pane"

# Refresh sidebars
sleep 0.1
for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null || true
done

exit 0
