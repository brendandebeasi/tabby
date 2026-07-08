#!/usr/bin/env bash
# Self-contained VM test: bring up an 80-col (PHONE profile, <100) session, then
# socket-inject a window_header:hamburger and confirm the daemon launches the
# full-width sidebar-popup OVERLAY (HAMBURGER_FULLWIDTH_POPUP in the log + a live
# sidebar-popup process) instead of stashing the inline sidebar.
set -u
REPO=/Users/b/git/tabby
L=tbham
export PATH=/usr/local/go/bin:$PATH

pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1
sleep 1

# Build the binary fresh on the VM.
( cd "$REPO" && go build -o bin/tabby ./cmd/tabby ) || { echo "BUILD FAIL"; exit 1; }

tmux -L $L new-session -d -s t -x 80 -y 40   # 80 < 100 -> phone
python3 -c "
import pty,os,time
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None)
    os.execvp('tmux',['tmux','-L','$L','attach','-t','t'])
time.sleep(90)
" >/dev/null 2>&1 &
CLIENT=$!
sleep 1.5
tmux -L $L run-shell -b "$REPO/tabby.tmux"
sleep 10

WIN=$(tmux -L $L display -p -t t "#{window_id}")
SESS=$(tmux -L $L display -p -t t "#{session_id}")
SOCK="/tmp/tabby-daemon-${SESS}.sock"
echo "window=$WIN session=$SESS sock=$SOCK width=$(tmux -L $L display -p -t t '#{window_width}')"

# Inject window_header:hamburger for this window.
python3 - "$SOCK" "$WIN" <<'PY'
import socket,json,sys,time
sock,win=sys.argv[1],sys.argv[2]
msg={"type":"input","target":{"kind":"window-header","window":win},
     "payload":{"type":"action","action":"press","button":"left",
                "resolved_action":"window_header:hamburger"}}
s=socket.socket(socket.AF_UNIX); s.connect(sock)
s.sendall((json.dumps(msg)+"\n").encode()); time.sleep(0.6); s.close()
print("injected window_header:hamburger on",win)
PY
sleep 2

echo "=== daemon log: HAMBURGER lines ==="
for f in /tmp/tabby-daemon-*-events.log; do grep -h "HAMBURGER" "$f" 2>/dev/null | tail -4; done
echo "=== sidebar-popup process alive? ==="
pgrep -af "sidebar-popup" | head
echo "=== popup client / display-popup ==="
tmux -L $L list-clients -F "tty=#{client_tty} #{client_width}x#{client_height}" 2>/dev/null

kill $CLIENT 2>/dev/null
tmux -L $L kill-server >/dev/null 2>&1
