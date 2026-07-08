set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders; L=tbrun
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1; sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x 120 -y 40
python3 -c "import pty,os,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None); os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
time.sleep(120)" >/dev/null 2>&1 &
C=$!; sleep 1.5
tmux -L $L set-option -g @tabby_custom_borders on
tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 9
SID=$(tmux -L $L display -p -t t "#{session_id}")
DP=$(ps -o pid=,command= -ax | grep -F "tabby daemon -session $SID" | grep -v grep | awk "{print \$1}" | head -1)
echo "daemon=$DP"
cnt() { tmux -L $L list-panes -t t -F "#{pane_start_command}" | grep -c "render pane-border"; }
echo "border panes after initial spawn: $(cnt)"
# Simulate many layout passes (this is what caused the runaway over time).
for i in 1 2 3 4 5 6; do kill -USR1 $DP 2>/dev/null; sleep 1.5; done
echo "border panes after 6 more passes: $(cnt)"
echo "content panes: $(tmux -L $L list-panes -t t -F '#{pane_start_command}' | grep -vc 'render ')"
kill $C 2>/dev/null; tmux -L $L kill-server >/dev/null 2>&1
