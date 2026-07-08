#!/usr/bin/env bash
# Self-contained VM test: brings up a box, splits into TWO stacked content panes
# (each gets a top edge via the multi-pane fallback), then socket-injects a
# top-edge drag_resize on the LOWER pane and checks the pane heights change AND
# PERSIST (repin only touches border panes, so a content resize should stick).
# Runs entirely in one shell so the pty keep-alive client never outlives it.
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
L=tbtest
export PATH=/usr/local/go/bin:$PATH

pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1
sleep 1

tmux -L $L new-session -d -s t -x 120 -y 40
# pty keep-alive client (kept in THIS shell's job table)
python3 -c "
import pty,os,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None)
    os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
time.sleep(120)
" >/dev/null 2>&1 &
CLIENT=$!
sleep 1.5
tmux -L $L run-shell -b "$WT/tabby.tmux"
sleep 10   # let the daemon spawn chrome

# Split the sole content pane into two stacked content panes.
CONTENT=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '!/render /{print $1; exit}')
echo "initial content pane=$CONTENT"
tmux -L $L split-window -v -t "$CONTENT"
sleep 8    # let the daemon spawn top edges for both content panes

echo "=== panes after split ==="
tmux -L $L list-panes -t t -F "#{pane_id} #{pane_top} #{pane_height} :: #{pane_start_command}" | sed -E 's/ :: .*-edge .([a-z]+).*/ [edge:\1]/; s/ :: $/ [content]/; s/ :: .*/ [content]/'

# The two content panes (no "render") sorted by top.
mapfile -t CP < <(tmux -L $L list-panes -t t -F "#{pane_top} #{pane_id} #{pane_start_command}" | awk '!/render /{print $1" "$2}' | sort -n | awk '{print $2}')
TOPPANE=${CP[0]:-}
BOTPANE=${CP[1]:-}
echo "top content=$TOPPANE  bottom content=$BOTPANE"
if [ -z "$BOTPANE" ]; then echo "SPLIT FAILED (only one content pane)"; kill $CLIENT 2>/dev/null; tmux -L $L kill-server 2>/dev/null; exit 1; fi

H_top0=$(tmux -L $L display -p -t "$TOPPANE" "#{pane_height}")
H_bot0=$(tmux -L $L display -p -t "$BOTPANE" "#{pane_height}")
echo "heights BEFORE: top=$H_top0 bottom=$H_bot0"

SESS=$(tmux -L $L display -p -t t "#{session_id}")
SOCK="/tmp/tabby-daemon-${SESS}.sock"
# Drag the BOTTOM pane's TOP edge UP (dy=-4) -> bottom pane grows, top shrinks.
python3 - "$SOCK" "$BOTPANE" <<'PY'
import socket,json,sys,time
sock,pane=sys.argv[1],sys.argv[2]
msg={"type":"input","target":{"kind":"pane-border","pane":pane,"edge":"top"},
     "payload":{"type":"action","action":"motion","button":"left",
                "resolved_action":"drag_resize","resolved_target":pane,
                "drag_edge":"top","drag_dy":-4,"pane_id":pane}}
s=socket.socket(socket.AF_UNIX); s.connect(sock)
s.sendall((json.dumps(msg)+"\n").encode()); time.sleep(0.5); s.close()
print("injected top-edge drag dy=-4 on",pane)
PY
sleep 3   # let a couple of reconcile passes run
H_top1=$(tmux -L $L display -p -t "$TOPPANE" "#{pane_height}")
H_bot1=$(tmux -L $L display -p -t "$BOTPANE" "#{pane_height}")
echo "heights AFTER : top=$H_top1 bottom=$H_bot1"
echo "RESULT: top delta=$((H_top1-H_top0))  bottom delta=$((H_bot1-H_bot0))  (expect bottom +~4, top -~4, PERSISTED)"

kill $CLIENT 2>/dev/null
tmux -L $L kill-server >/dev/null 2>&1
