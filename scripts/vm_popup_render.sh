#!/usr/bin/env bash
# Verify the sidebar-popup renderer: fills the whole surface with the sidebar bg
# (no dark gaps) and shows a bottom tap-to-close bar. Runs the renderer in a
# normal split pane (display-popup itself isn't capture-able) and captures it.
set -u
REPO=/Users/b/git/tabby
L=tbpop
export PATH=/usr/local/go/bin:$PATH

pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1
sleep 1
( cd "$REPO" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }

tmux -L $L new-session -d -s t -x 90 -y 30
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
sleep 9
SESS=$(tmux -L $L display -p -t t "#{session_id}")

# Spawn the popup renderer in a split pane we can capture.
tmux -L $L split-window -h -t t -l 40 "exec -a tabby-sidebar-popup $REPO/bin/tabby render sidebar-popup --session '$SESS'"
sleep 4
POP=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '/sidebar-popup/{print $1; exit}')
echo "popup pane=$POP  size=$(tmux -L $L display -p -t "$POP" '#{pane_width}x#{pane_height}')"
echo "=== rendered (with ANSI, cat -v on first+last rows) ==="
tmux -L $L capture-pane -p -e -t "$POP" 2>/dev/null | sed -n '1p;$p' | cat -v | cut -c1-160
echo "=== plain last 2 rows (close bar?) ==="
tmux -L $L capture-pane -p -t "$POP" 2>/dev/null | tail -2
echo "=== close button present (bottom block)? ==="
tmux -L $L capture-pane -p -t "$POP" 2>/dev/null | tail -4 | grep -q "Close" && echo "CLOSE BAR: yes" || echo "CLOSE BAR: MISSING"
echo "=== any bg color escape present (48;2 / 48;5)? ==="
tmux -L $L capture-pane -p -e -t "$POP" 2>/dev/null | grep -qE '\[48[;:]' && echo "BG FILL: yes" || echo "BG FILL: none"

kill $CLIENT 2>/dev/null
tmux -L $L kill-server >/dev/null 2>&1
