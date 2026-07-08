#!/usr/bin/env bash
# Visual+interaction harness. Usage: vm-harness.sh [desktop|phone]
#   desktop: box, tabs, split/close buttons, drag-to-resize between panes.
#   phone:   80-col, window-header hamburger -> full-width sidebar popup.
# Xvfb + xterm + xdotool + labeled screenshots in scripts/shots/.
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
SCEN="${1:-desktop}"
L=harness; DISP=":95"
[ "$SCEN" = phone ] && COLS=80 || COLS=120
ROWS=40
SHOTS="$WT/scripts/shots"; mkdir -p "$SHOTS"; rm -f "$SHOTS"/*.png
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" 2>/dev/null; pkill -9 -f "tabby daemon" 2>/dev/null
pkill -f "Xvfb $DISP" 2>/dev/null; rm -f /tmp/.X95-lock /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock
sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x $COLS -y $ROWS
if [ "$SCEN" = desktop ]; then tmux -L $L set-option -g @tabby_custom_borders on; else tmux -L $L set-option -g @tabby_custom_borders off; fi
tmux -L $L set-option -g allow-passthrough on
Xvfb $DISP -screen 0 $(( COLS*9+40 ))x680x24 >/tmp/xvfb.log 2>&1 & XVFB=$!
sleep 1.2
DISPLAY=$DISP xterm -ti vt340 +sb -geometry ${COLS}x${ROWS} -fa 'DejaVu Sans Mono' -fs 11 -bg black -fg white \
  -e tmux -L $L attach -t t >/tmp/xterm.log 2>&1 & XTERM=$!
sleep 2
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 8
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk '{print $1}' | head -1)
chrome(){ tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c "render $1"; }
for i in $(seq 1 15); do tmux -L $L refresh-client 2>/dev/null; kill -USR1 $DP 2>/dev/null; sleep 2
  if [ "$SCEN" = phone ]; then [ "$(chrome window-header)" -gt 0 ] && break; else [ "$(chrome pane-border)" -gt 0 ] && break; fi; done
echo "scen=$SCEN edges=$(chrome pane-border) wheader=$(chrome window-header)"
WIN=$(DISPLAY=$DISP xdotool search --class XTerm | head -1); eval $(DISPLAY=$DISP xdotool getwindowgeometry --shell $WIN)
CW=$(( WIDTH / COLS )); CH=$(( HEIGHT / ROWS )); echo "cell=${CW}x${CH} @${X},${Y}"
shot(){ DISPLAY=$DISP import -window root "$SHOTS/$1.png" 2>/dev/null; echo "  $1"; }
clickcell(){ local px=$(( X + $1*CW + CW/2 )) py=$(( Y + $2*CH + CH/2 )); DISPLAY=$DISP xdotool mousemove $px $py click 1; sleep 1.5; }
dragpx(){ DISPLAY=$DISP xdotool mousemove $1 $2 mousedown 1; sleep .4; DISPLAY=$DISP xdotool mousemove $3 $4; sleep .4; DISPLAY=$DISP xdotool mouseup 1; sleep 1.5; }
heights(){ tmux -L $L list-panes -t t -F '#{pane_id} #{pane_top} #{pane_height} #{pane_start_command}' | grep -v 'render ' | awk '{print $1"@top"$2"h"$3}' | tr '\n' ' '; }

if [ "$SCEN" = desktop ]; then
  shot 01_initial
  clickcell 115 0; shot 02_split_v          # split into two stacked panes
  echo "  before: $(heights)"
  # find the LOWER content pane's top edge row (its pane_top-1) to drag it
  LOWTOP=$(tmux -L $L list-panes -t t -F '#{pane_top} #{pane_id} #{pane_start_command}' | grep -v 'render ' | sort -n | tail -1 | awk '{print $1}')
  EDGEROW=$(( LOWTOP - 1 )); echo "  dragging edge at row $EDGEROW up 6"
  DX=$(( X + 60*CW )); dragpx $DX $(( Y + EDGEROW*CH + CH/2 )) $DX $(( Y + (EDGEROW-6)*CH + CH/2 ))
  echo "  after:  $(heights)"; shot 03_after_drag
else
  shot 01_phone_initial
  WIN0=$(tmux -L $L display -p -t t "#{window_id}")
  SOCK="/tmp/tabby-daemon-${SID}.sock"
  python3 "$WT/scripts/inject_action.py" "$SOCK" window-header "$WIN0" window_header:hamburger; sleep 7
  shot 02_hamburger_popup
  echo "  popup proc: $(pgrep -af sidebar-popup | head -1)"
  LOG=$(ls -t /tmp/tabby-daemon-*-events.log 2>/dev/null|head -1); echo "  --- log ---"; grep -hiE "HAMBURGER|WINDOW_HEADER_ACTION|hamburger" "$LOG" 2>/dev/null | tail -4; echo "  active clients:"; tmux -L $L list-clients -F "    #{client_tty} #{client_width}x#{client_height}"
fi
kill $XTERM $XVFB 2>/dev/null; tmux -L $L kill-server 2>/dev/null; echo DONE
