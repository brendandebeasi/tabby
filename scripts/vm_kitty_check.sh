set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders; L=tbk
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1; sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x 120 -y 40
python3 -c "
import pty,os,time,fcntl,termios,struct
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None); os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH',40,120,0,0))
time.sleep(60)" >/dev/null 2>&1 &
C=$!; sleep 1.5
tmux -L $L set-option -g @tabby_custom_borders on
tmux -L $L set-option -g allow-passthrough on
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 9
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk '{print $1}' | head -1)
TOP=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '/-edge .top/{print $1; exit}')
echo "daemon=$DP top=$TOP"
chk() { tmux -L $L capture-pane -p -t "$TOP" 2>/dev/null | head -1 | grep -q CPB && echo GLYPHS || echo SWITCHED; }
echo "OFF: $(chk)"
tmux -L $L set-option -g @tabby_border_graphics kitty; kill -USR1 $DP 2>/dev/null; sleep 3; echo "kitty: $(chk)"
tmux -L $L set-option -g @tabby_border_graphics sixel; kill -USR1 $DP 2>/dev/null; sleep 3; echo "sixel: $(chk)"
tmux -L $L set-option -g @tabby_border_graphics off;   kill -USR1 $DP 2>/dev/null; sleep 3; echo "off:   $(chk)"
kill $C 2>/dev/null; tmux -L $L kill-server >/dev/null 2>&1
