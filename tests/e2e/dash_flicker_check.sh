#!/usr/bin/env bash
set -u
PLUG=$HOME/.tmux/plugins/tabby; TMUXB="tmux -L tabbytest"; BIN="$PLUG/bin/tabby"; O=/tmp/flick.txt; : >$O
pkill -9 -f "tabby/bin/tabby" 2>/dev/null; $TMUXB kill-server 2>/dev/null
rm -f /tmp/tabby-daemon-*; sleep 1
$TMUXB new-session -d -s main -x 200 -y 50
$TMUXB run-shell -b "$PLUG/tabby.tmux" >/dev/null 2>&1
for _ in $(seq 1 50); do ls /tmp/tabby-daemon-*.sock >/dev/null 2>&1 && break; sleep 0.2; done; sleep 1
W1=$($TMUXB display-message -p -t main "#{window_id}"); $TMUXB split-window -d -h -t "$W1"
W2=$($TMUXB new-window -P -F "#{window_id}"); $TMUXB split-window -d -v -t "$W2"
$TMUXB select-window -t "$W1"; sleep 3
$TMUXB run-shell "$BIN dashboard" >/dev/null 2>&1; sleep 4
DASH=$($TMUXB list-windows -F '#{window_id}|#{@tabby_dashboard}' | awk -F'|' '$2=="1"{print $1}')
SB=$($TMUXB list-panes -t "$DASH" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' | awk -F'|' '/sidebar/{print $1; exit}')
dump() {
  echo "=== $1 ==="
  echo "sidebar $SB: top=$($TMUXB display-message -t $SB -p '#{pane_top}') height=$($TMUXB display-message -t $SB -p '#{pane_height}') width=$($TMUXB display-message -t $SB -p '#{pane_width}')"
  $TMUXB list-panes -t "$DASH" -F "  pane=#{pane_id} top=#{pane_top} height=#{pane_height} active=#{pane_active}"
}
{
  dump "INITIAL (active after gather)"
  for i in 1 2 3 4; do
    $TMUXB run-shell "$BIN hook next-window" >/dev/null 2>&1
    sleep 0.4
    dump "AFTER cycle #$i (active=$($TMUXB display-message -t $DASH -p '#{pane_id}'))"
  done
} >>$O 2>&1
