#!/usr/bin/env bash
# Visual + interaction harness (one shell): Xvfb+xterm client FIRST (desktop width)
# -> tabby daemon -> box -> xdotool clicks/drags -> labeled screenshots in scripts/shots/.
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
L=harness; DISP=":95"; COLS=120; ROWS=40
SHOTS="$WT/scripts/shots"; mkdir -p "$SHOTS"; rm -f "$SHOTS"/*.png
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" 2>/dev/null; pkill -9 -f "tabby daemon" 2>/dev/null
pkill -f "Xvfb $DISP" 2>/dev/null; rm -f /tmp/.X95-lock /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock
sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x $COLS -y $ROWS
tmux -L $L set-option -g @tabby_custom_borders on
tmux -L $L set-option -g allow-passthrough on
# xterm (wide desktop client) attaches BEFORE the daemon starts.
Xvfb $DISP -screen 0 1300x680x24 >/tmp/xvfb.log 2>&1 & XVFB=$!
sleep 1.2
DISPLAY=$DISP xterm -ti vt340 +sb -geometry ${COLS}x${ROWS} -fa 'DejaVu Sans Mono' -fs 11 -bg black -fg white \
  -e tmux -L $L attach -t t >/tmp/xterm.log 2>&1 & XTERM=$!
sleep 2
echo "client width=$(tmux -L $L display -p -t t '#{client_width}')"
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 10
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk '{print $1}' | head -1)
for i in $(seq 1 12); do tmux -L $L refresh-client 2>/dev/null; kill -USR1 $DP 2>/dev/null; sleep 2; [ "$(tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c 'render pane-border')" -gt 0 ] && break; done
LOG=$(ls -t /tmp/tabby-daemon-*-events.log 2>/dev/null|head -1)
echo "edges=$(tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c 'render pane-border') daemon=$DP"
grep -hE "PANE_LAYOUT_START" "$LOG" 2>/dev/null | tail -2
WIN=$(DISPLAY=$DISP xdotool search --class XTerm | head -1)
eval $(DISPLAY=$DISP xdotool getwindowgeometry --shell $WIN)
CW=$(( WIDTH / COLS )); CH=$(( HEIGHT / ROWS ))
echo "win ${WIDTH}x${HEIGHT} @${X},${Y} cell=${CW}x${CH}"
shot(){ DISPLAY=$DISP import -window root "$SHOTS/$1.png" 2>/dev/null; echo "  shot $1"; }
setopt(){ tmux -L $L set-option -g "$1" "$2"; kill -USR1 $DP 2>/dev/null; sleep 2.5; }
shot 01_initial_box
setopt @tabby_border_graphics blocks; shot 02_blocks
setopt @tabby_border_graphics off;    shot 03_glyphs
kill $XTERM $XVFB 2>/dev/null; tmux -L $L kill-server 2>/dev/null
echo "DONE"
