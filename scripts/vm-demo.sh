#!/usr/bin/env bash
# One-command demo/toggle harness for the custom pane borders on the tabby-dev VM.
#
# Run everything through orb from the Mac, e.g.:
#   orb -m tabby-dev bash -lc 'bash /Users/b/git/tabby/.claude/worktrees/custom-pane-borders/scripts/vm-demo.sh up'
#   orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh borders off'
#   orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh sixel on'
#   orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh down'
# Then ATTACH from a real terminal to SEE it (sixel needs a graphics-capable term):
#   orb -m tabby-dev tmux -L tbdemo attach -t demo
#
# Subcommands:
#   up            build + start the demo session (box on), keep it alive
#   borders on|off   toggle the custom box (off = native tmux borders)
#   sixel on|off     toggle graphics in the top edge
#   status        show current toggle state + border panes
#   attach        print the attach command
#   down          tear everything down
set -u
WT=/Users/b/git/tabby/.claude/worktrees/custom-pane-borders
L=tbdemo
export PATH=/usr/local/go/bin:$PATH

sid()  { tmux -L $L display -p -t demo "#{session_id}" 2>/dev/null; }
# session_id is literally "$N" (the $ breaks pgrep regex) -> fixed-string match.
dpid() { local s; s=$(sid); [ -n "$s" ] && ps -o pid=,command= -ax | grep -F "tabby daemon -session $s" | grep -v grep | awk '{print $1}' | head -1; }
nudge() {
	# Re-run the layout pass so a toggle takes effect: SIGUSR1 the daemon.
	local p; p=$(dpid); [ -n "$p" ] && kill -USR1 "$p" 2>/dev/null
	sleep 2
}
borders_count() { tmux -L $L list-panes -t demo -F "#{pane_start_command}" 2>/dev/null | grep -c "render pane-border"; }

case "${1:-}" in
up)
	pkill -9 -f "L $L" >/dev/null 2>&1; pkill -9 -f "tabby daemon" >/dev/null 2>&1
	rm -f /tmp/tmux-*/$L /tmp/tabby-daemon-*.sock >/dev/null 2>&1
	sleep 1
	( cd "$WT" && go build -o bin/tabby ./cmd/tabby ) || { echo "BUILD FAIL"; exit 1; }
	tmux -L $L new-session -d -s demo -x 100 -y 40
	tmux -L $L new-window -t demo -n editor
	tmux -L $L new-window -t demo -n logs
	tmux -L $L select-window -t demo:0
	# Long-lived keep-alive client so the daemon never idle-quits between toggles.
	# nohup+setsid+disown so it survives this shell (and the orb invocation) exiting.
	cat > /tmp/vm-demo-keepalive.py <<'PY'
import pty,os,time,sys
L=sys.argv[1]
pid,fd=pty.fork()
if pid==0:
    os.environ.pop('TMUX',None)
    os.execvp('tmux',['tmux','-L',L,'attach','-t','demo'])
time.sleep(36000)
PY
	nohup setsid python3 /tmp/vm-demo-keepalive.py "$L" >/dev/null 2>&1 < /dev/null &
	disown -a 2>/dev/null || true
	sleep 1.5
	tmux -L $L run-shell -b "$WT/tabby.tmux"
	sleep 8
	tmux -L $L set-option -g @tabby_custom_borders on
	# Nudge until the box spawns (daemon may still be warming up).
	for _ in 1 2 3 4 5; do
		nudge
		[ "$(borders_count)" -gt 0 ] && break
	done
	echo "demo up. border panes: $(borders_count)"
	echo "keep-alive client: $(pgrep -f vm-demo-keepalive | head -1)"
	echo "ATTACH from a real terminal to see it:"
	echo "    orb -m tabby-dev tmux -L $L attach -t demo"
	;;
borders)
	v="${2:-on}"; tmux -L $L set-option -g @tabby_custom_borders "$v"; nudge
	echo "borders=$v  ->  border panes: $(borders_count)  (0 = native)"
	;;
sixel)
	v="${2:-on}"
	tmux -L $L set-option -g allow-passthrough on
	tmux -L $L set-option -g @tabby_border_sixel "$v"; nudge
	echo "sixel=$v  (graphics in top edge; needs a sixel-capable terminal)"
	;;
status)
	echo "custom_borders = $(tmux -L $L show-option -gqv @tabby_custom_borders)"
	echo "border_sixel   = $(tmux -L $L show-option -gqv @tabby_border_sixel)"
	echo "border panes   = $(borders_count)"
	echo "daemon pid     = $(dpid)"
	;;
attach)
	echo "orb -m tabby-dev tmux -L $L attach -t demo"
	;;
down)
	pkill -9 -f "L $L" >/dev/null 2>&1
	tmux -L $L kill-server >/dev/null 2>&1
	pkill -9 -f "tabby daemon -session" >/dev/null 2>&1
	echo "demo down."
	;;
*)
	echo "usage: vm-demo.sh {up|borders on|off|sixel on|off|status|attach|down}"
	;;
esac
