#!/usr/bin/env bash
# Isolated live-test harness for the custom-pane-border feature on the tabby-dev VM.
#
# tmux plugins load via `run-shell` (NOT source-file — tabby.tmux is a bash
# script). tabby.tmux resolves all binaries from its own dir (CURRENT_DIR), so
# running THIS worktree's tabby.tmux uses THIS worktree's bin/. The daemon
# idle-quits ~30s without an attached client, so we keep a pty client attached
# via `script`.
#
# Usage (from the worktree root, on the VM):
#   make build && bash scripts/vm-border-test.sh
# then inspect:
#   tmux -L tbtest list-panes -a -F '#{pane_id}|#{pane_current_command}'
#   tmux -L tbtest capture-pane -p -t <pane-header-pane-id>
set -u
WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
L="${TABBY_TEST_SOCKET:-tbtest}"
LOG=/tmp/${L}.log
exec >"$LOG" 2>&1

tmux -L "$L" kill-server 2>/dev/null
pkill -f "tabby daemon" 2>/dev/null
sleep 1

tmux -L "$L" new-session -d -s t -x 140 -y 44
# Keep a real client attached (pty) so the daemon does not idle-quit.
setsid bash -c "TMUX= script -qec \"tmux -L $L attach -t t\" /tmp/${L}.pty" >/dev/null 2>&1 &
sleep 1

# Load tabby (spawns daemon + sidebar + custom pane-header chrome).
tmux -L "$L" run-shell "$WT/tabby.tmux"
sleep 6

echo "=== PANES ==="
tmux -L "$L" list-panes -a -F '#{pane_id}|top=#{pane_top}|#{pane_width}x#{pane_height}|cmd=#{pane_current_command}|start=#{pane_start_command}' | sed "s#$WT#WT#g"
echo "=== PANE-HEADER RENDER ==="
ph=$(tmux -L "$L" list-panes -a -F '#{pane_id}|#{pane_start_command}' | grep 'render pane-header' | head -1 | cut -d'|' -f1)
[ -n "$ph" ] && tmux -L "$L" capture-pane -p -t "$ph" | head -3
echo "=== DONE (log: $LOG) ==="
