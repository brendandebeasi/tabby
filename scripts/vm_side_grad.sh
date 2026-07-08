set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders; L=tbsg
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1; sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x 100 -y 30
python3 -c "import pty,os,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None); os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
time.sleep(70)" >/dev/null 2>&1 &
C=$!; sleep 1.5; tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 10
LEFT=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk "/-edge .left/{print \$1; exit}")
echo "left edge pane=$LEFT height=$(tmux -L $L display -p -t "$LEFT" '#{pane_height}')"
echo "=== per-row bg (top..bottom) ==="
tmux -L $L capture-pane -p -e -t "$LEFT" 2>/dev/null | grep -oE '48;2;[0-9]+;[0-9]+;[0-9]+' 
TOP=$(tmux -L $L capture-pane -p -e -t "$LEFT" 2>/dev/null | grep -oE '48;2;[0-9]+;[0-9]+;[0-9]+' | head -1)
BOT=$(tmux -L $L capture-pane -p -e -t "$LEFT" 2>/dev/null | grep -oE '48;2;[0-9]+;[0-9]+;[0-9]+' | tail -1)
echo "top=$TOP bottom=$BOT"
[ -n "$TOP" ] && [ "$TOP" != "$BOT" ] && echo "SIDE GRADIENT: yes" || echo "SIDE GRADIENT: flat/none"
kill $C 2>/dev/null; tmux -L $L kill-server >/dev/null 2>&1
