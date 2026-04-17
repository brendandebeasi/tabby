#!/usr/bin/env bash
# signal-daemon.sh [session_id]
# Send SIGUSR1 to the tabby daemon for the given (or current) tmux session.
#
# When called from tmux hooks without args, tmux format expansion of
# #{session_id} produces strings like "$5" which bash misinterprets as
# positional variables.  This script looks up the session ID directly
# via tmux display-message to avoid that entirely.
SESSION="${1:-}"
if [ -z "$SESSION" ]; then
    SESSION=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
fi
[ -z "$SESSION" ] && exit 0
PID=$(cat "/tmp/tabby-daemon-${SESSION}.pid" 2>/dev/null)
[ -n "$PID" ] && kill -USR1 "$PID" 2>/dev/null || true
