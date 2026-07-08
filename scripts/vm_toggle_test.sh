set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders; L=tbtog
export PATH=/usr/local/go/bin:$PATH
pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1; sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }
tmux -L $L new-session -d -s t -x 100 -y 30
python3 -c "import pty,os,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None); os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
time.sleep(80)" >/dev/null 2>&1 &
C=$!; sleep 1.5; tmux -L $L run-shell -b "$WT/tabby.tmux"; sleep 10
CONTENT=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '!/render /{print $1; exit}')
N0=$(tmux -L $L list-panes -t t -F "#{pane_start_command}" | grep -c "render pane-border")
echo "content pane=$CONTENT   border panes BEFORE opt-out: $N0"
echo "-> set @tabby_border_enable=0 on $CONTENT, then trigger a layout pass"
tmux -L $L set-option -p -t "$CONTENT" @tabby_border_enable 0
# Trigger doPaneLayoutOps via a split (adds a pane => layout reconcile).
tmux -L $L split-window -v -t "$CONTENT"; sleep 6
# Border edges belonging to the DISABLED pane $CONTENT:
DIS=$(tmux -L $L list-panes -t t -F "#{pane_start_command}" | grep -c "render pane-border.*-pane .$CONTENT")
echo "border edges still on disabled pane $CONTENT: $DIS  (expect 0)"
OTHER=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '!/render /{print $1}' | grep -v "$CONTENT" | head -1)
OE=$(tmux -L $L list-panes -t t -F "#{pane_start_command}" | grep -c "render pane-border.*-pane .$OTHER")
echo "border edges on the OTHER (enabled) pane $OTHER: $OE  (expect >=1)"
[ "$DIS" = "0" ] && [ "$OE" -ge 1 ] && echo "TOGGLE: yes (disabled pane has no box, enabled pane does)" || echo "TOGGLE: FAILED"
kill $C 2>/dev/null; tmux -L $L kill-server >/dev/null 2>&1
