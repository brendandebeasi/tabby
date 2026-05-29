#!/usr/bin/env bash
# Sets up a ready-to-attach tabby session in the dev machine for hands-on
# testing of the pane dashboard. Uses the DEFAULT tmux socket so you can attach
# with a plain `tmux attach`. Leaves everything running (no cleanup trap).
#
# Usage (from the Mac host):
#   orb -m tabby-dev bash /Users/b/git/tabby/tests/e2e/dashboard_interactive.sh
#   orb -m tabby-dev                       # drop into a shell in the machine
#   tmux attach -t main                    # then play
#
# In tmux:  prefix (Ctrl-b) g   = toggle dashboard
#           prefix z            = zoom a tile (dive in) / unzoom
#           prefix arrows       = move between / resize panes
#           prefix x            = close a pane
set -u
PLUG="${PLUG:-$HOME/.tmux/plugins/tabby}"
BIN="$PLUG/bin/tabby"

pkill -f "tabby watchdog" 2>/dev/null || true
pkill -f "tabby daemon"   2>/dev/null || true
tmux kill-server 2>/dev/null || true
rm -f /tmp/tabby-daemon-*.sock /tmp/tabby-daemon-*.pid /tmp/tabby-daemon-*.watchdog.pid /tmp/tabby-daemon-*.clean-stop 2>/dev/null || true

tmux new-session -d -s main -x 200 -y 50
tmux run-shell -b "$PLUG/tabby.tmux" >/dev/null 2>&1
for _ in $(seq 1 50); do ls /tmp/tabby-daemon-*.sock >/dev/null 2>&1 && break; sleep 0.2; done
sleep 1

# A small workspace: 3 windows with a couple panes each, each pane labelled so
# you can see them rearrange in the grid.
W1=$(tmux display-message -p -t main "#{window_id}")
tmux send-keys -t "$W1" "echo W1-pane-A; " Enter
tmux split-window -d -h -t "$W1"; tmux send-keys -t "$W1.1" "echo W1-pane-B; " Enter
W2=$(tmux new-window -P -F "#{window_id}"); tmux send-keys -t "$W2" "echo W2-pane-A; " Enter
tmux split-window -d -v -t "$W2"; tmux send-keys -t "$W2.1" "echo W2-pane-B; " Enter
W3=$(tmux new-window -P -F "#{window_id}"); tmux send-keys -t "$W3" "echo W3-pane-A; " Enter
tmux select-window -t "$W1"

# Temporary test binding: prefix + g toggles the dashboard.
tmux bind-key g run-shell "$BIN dashboard"

echo "Ready. Attach with:  orb -m tabby-dev   then   tmux attach -t main"
echo "Toggle dashboard:  prefix (Ctrl-b) then g"
