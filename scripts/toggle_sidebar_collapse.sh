#!/usr/bin/env bash
# Toggle sidebar collapse/expand via daemon socket.
# The daemon decides collapse vs expand based on internal state,
# eliminating race conditions from reading tmux options.

set -eu

SESSION_ID=$(tmux display-message -p '#{session_id}')
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"

if [ ! -S "$DAEMON_SOCK" ]; then
    exit 0
fi

MSG='{"type":"input","client_id":"","payload":{"type":"action","resolved_action":"toggle_collapse_sidebar","resolved_target":""}}'
python3 -c "
import socket, time
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(2)
s.connect('$DAEMON_SOCK')
s.sendall(b'$MSG\n')
time.sleep(0.3)
s.close()
" >/dev/null 2>&1 || true
