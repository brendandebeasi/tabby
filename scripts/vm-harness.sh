#!/usr/bin/env bash
# Full visual+interaction harness (one shell): Xvfb + xterm, box on, 3 windows.
# Real xdotool clicks/drags + labeled screenshots in scripts/shots/.
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
tmux -L $L new-window -t t -c "$WT"; tmux -L $L new-window -t t -c /tmp
tmux -L $L select-window -t t:1
tmux -L $L set-option -g @tabby_custom_borders on
tmux -L $L set-option -g allow-passthrough on
Xvfb $DISP -screen 0 1300x680x24 >/tmp/xvfb.log 2>&1 & XVFB=$!
sleep 1.2
DISPLAY=$DISP xterm -ti vt340 +sb -geometry ${COLS}x${ROWS} -fa 'DejaVu Sans Mono' -fs 11 -bg black -fg white \
  -e tmux -L $L attach -t t >/tmp/xterm.log 2>&1 & XTERM=$!
sleep 2
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 8
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk '{print $1}' | head -1)
ecount(){ tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c 'render pane-border'; }
ccount(){ tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -vc 'render '; }
for i in $(seq 1 15); do tmux -L $L refresh-client 2>/dev/null; kill -USR1 $DP 2>/dev/null; sleep 2; [ "$(ecount)" -gt 0 ] && break; done
echo "edges=$(ecount) daemon=$DP"
WIN=$(DISPLAY=$DISP xdotool search --class XTerm | head -1)
eval $(DISPLAY=$DISP xdotool getwindowgeometry --shell $WIN)
CW=$(( WIDTH / COLS )); CH=$(( HEIGHT / ROWS ))
echo "cell=${CW}x${CH} @${X},${Y}"
shot(){ DISPLAY=$DISP import -window root "$SHOTS/$1.png" 2>/dev/null; echo "  $1: win=$(tmux -L $L display -p '#{window_index}') content_panes=$(ccount)"; }
clickcell(){ local px=$(( X + $1*CW + CW/2 )) py=$(( Y + $2*CH + CH/2 )); DISPLAY=$DISP xdotool mousemove $px $py click 1; sleep 1.5; }
dragcell(){ local x1=$(( X+$1*CW+CW/2 )) y1=$(( Y+$2*CH+CH/2 )) x2=$(( X+$3*CW+CW/2 )) y2=$(( Y+$4*CH+CH/2 ))
  DISPLAY=$DISP xdotool mousemove $x1 $y1 mousedown 1; sleep .4; DISPLAY=$DISP xdotool mousemove $x2 $y2; sleep .4; DISPLAY=$DISP xdotool mouseup 1; sleep 1.5; }
setopt(){ tmux -L $L set-option -g "$1" "$2"; kill -USR1 $DP 2>/dev/null; sleep 2.5; }

# ---- scenario ----
shot 01_initial
clickcell 6 7;  shot 02_tab2_active
clickcell 6 6;  shot 03_tab1_active
clickcell 113 0; shot 04_split_h     # split-h button
clickcell 115 0; shot 05_split_v     # split-v button
shot 06_after_splits
clickcell 117 0; shot 07_close       # close button
setopt @tabby_border_graphics blocks; shot 08_blocks
setopt @tabby_border_graphics off;    shot 09_glyphs
kill $XTERM $XVFB 2>/dev/null; tmux -L $L kill-server 2>/dev/null
echo DONE
