#!/usr/bin/env bash
# Toggle sidebar collapse/expand via daemon socket
# Collapses sidebar to 1-column ">" strip, or expands it back.
# Does NOT kill/restart the daemon (unlike toggle_sidebar.sh).

set -eu

SESSION_ID=$(tmux display-message -p '#{session_id}')
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"

if [ ! -S "$DAEMON_SOCK" ]; then
    exit 0  # Daemon not running, nothing to do
fi

# Check current collapse state
COLLAPSED=$(tmux show-option -gqv @tabby_sidebar_collapsed 2>/dev/null || echo "")

if [ "$COLLAPSED" = "1" ]; then
    ACTION="expand_sidebar"
else
    ACTION="collapse_sidebar"
fi

# Send action to daemon via Unix socket (JSON + newline framing)
# python3 required: nc -U disconnects before the daemon's scanner reads the line
MSG='{"type":"input","client_id":"","payload":{"type":"action","resolved_action":"'"$ACTION"'","resolved_target":""}}'
python3 -c "
import socket, time
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(2)
s.connect('$DAEMON_SOCK')
s.sendall(b'$MSG\n')
time.sleep(0.3)
s.close()
" >/dev/null 2>&1 || true
