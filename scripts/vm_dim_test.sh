#!/usr/bin/env bash
# Verify inactive-pane border dimming: split into two stacked content panes, then
# compare the ACTIVE vs INACTIVE pane's top-edge background colour. With
# dim_inactive:true the inactive pane's box should be desaturated (different RGB).
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
L=tbdim
export PATH=/usr/local/go/bin:$PATH

pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1
sleep 1
( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo BUILD FAIL; exit 1; }

tmux -L $L new-session -d -s t -x 100 -y 40
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
tmux -L $L run-shell -b "$WT/tabby.tmux"
sleep 10

CONTENT=$(tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk '!/render /{print $1; exit}')
tmux -L $L split-window -v -t "$CONTENT"
sleep 8
# Make the FIRST content pane active (deterministic).
mapfile -t CP < <(tmux -L $L list-panes -t t -F "#{pane_top} #{pane_id} #{pane_start_command}" | awk '!/render /{print $1" "$2}' | sort -n | awk '{print $2}')
ACTIVE=${CP[0]}; INACTIVE=${CP[1]}
tmux -L $L select-pane -t "$ACTIVE"
sleep 3

# For each content pane, find its top-edge border pane and grab the first bg RGB.
topedge() { tmux -L $L list-panes -t t -F "#{pane_id} #{pane_start_command}" | awk -v p="$1" '$0 ~ ("-pane ."p"..? -edge .top") {print $1; exit}'; }
firstbg() { tmux -L $L capture-pane -p -e -t "$1" 2>/dev/null | head -1 | grep -oE '48;2;[0-9]+;[0-9]+;[0-9]+' | head -1; }

AE=$(topedge "$ACTIVE"); IE=$(topedge "$INACTIVE")
echo "active pane=$ACTIVE top-edge=$AE"
echo "inactive pane=$INACTIVE top-edge=$IE"
ABG=$(firstbg "$AE"); IBG=$(firstbg "$IE")
echo "active   top-edge first bg: $ABG"
echo "inactive top-edge first bg: $IBG"
if [ -n "$ABG" ] && [ -n "$IBG" ]; then
  [ "$ABG" != "$IBG" ] && echo "DIMMING: yes (inactive differs)" || echo "DIMMING: NO (same colour)"
else
  echo "DIMMING: could not read colours"
fi

kill $CLIENT 2>/dev/null
tmux -L $L kill-server >/dev/null 2>&1
