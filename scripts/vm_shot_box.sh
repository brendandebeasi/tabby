#!/usr/bin/env bash
# vm_shot_box.sh <graphics-mode> <out.png> [terminal]
#   graphics-mode: off|sixel|kitty|auto ; terminal: xterm(default)|kitty
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
MODE="${1:-off}"; OUT="${2:-$WT/scripts/_box.png}"; TERM_="${3:-xterm}"
L=tbshot; DISP=":99"
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
pkill -f "Xvfb $DISP" >/dev/null 2>&1; rm -f /tmp/.X99-lock
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock; sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x 120 -y 40
tmux -L $L set-option -g @tabby_custom_borders on
tmux -L $L set-option -g allow-passthrough on
[ "$MODE" != off ] && tmux -L $L set-option -g @tabby_border_graphics "$MODE"
tmux -L $L set-window-option -t t @tabby_color "#e0561f"
python3 -c "
import pty,os,time,fcntl,termios,struct
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None); os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH',40,120,0,0))
time.sleep(40)" >/dev/null 2>&1 &
PTY=$!; sleep 1.5
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 9
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk '{print $1}' | head -1)
for i in 1 2 3; do [ -n "$DP" ] && kill -USR1 $DP 2>/dev/null; sleep 2; done
echo "edges after pty spawn: $(tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c 'render pane-border')"
Xvfb $DISP -screen 0 2200x800x24 >/tmp/xvfb.log 2>&1 & XVFB=$!
sleep 1.2
if [ "$TERM_" = kitty ]; then
  LIBGL_ALWAYS_SOFTWARE=1 DISPLAY=$DISP kitty --config NONE \
    -o font_family="DejaVu Sans Mono" -o font_size=12 -o background=#000000 \
    -o initial_window_width=120c -o initial_window_height=40c \
    bash -c "tmux -L $L attach -t t" >/tmp/xterm.log 2>&1 & TERMPID=$!
else
  DISPLAY=$DISP xterm -ti vt340 -xrm 'XTerm*decTerminalID: vt340' -xrm 'XTerm*sixelScrolling: true' \
    -geometry 120x40 -fa 'DejaVu Sans Mono' -fs 12 -bg black -fg white \
    -e tmux -L $L attach -t t >/tmp/xterm.log 2>&1 & TERMPID=$!
fi
sleep 4; kill $PTY >/dev/null 2>&1
[ -n "$DP" ] && kill -USR1 $DP; sleep 4
echo "mode=$MODE term=$TERM_ termfeatures=[$(tmux -L $L display -p '#{client_termfeatures}')] edges=$(tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -c 'render pane-border')"
DISPLAY=$DISP import -window root "$OUT" 2>/dev/null
echo "shot: $OUT ($(wc -c < "$OUT" 2>/dev/null) bytes)"
kill $TERMPID $XVFB >/dev/null 2>&1; tmux -L $L kill-server >/dev/null 2>&1
