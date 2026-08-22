#!/usr/bin/env bash
# ensure-daemon.sh [session_id] [session_name]
# Start the tabby daemon/watchdog for the given (or current) tmux session if
# one is not already running.
#
# tabby runs one daemon per session, keyed by session id
# (/tmp/tabby-daemon-$ID.sock). tabby.tmux only ensures a daemon at plugin
# source time, for whatever session was current then, so any session created
# later — most easily a grouped clone (`new-session -t <group>`), which shares
# the window list and therefore looks completely normal — had no daemon and
# never would. Rendering still worked there (renderer panes belong to the
# shared windows and are already connected to the original session's daemon)
# while every input hook re-derived its socket path from the current session
# id, found nothing, and dropped the keypress after exhausting its retries.
#
# Called from session-created and client-attached so a session cannot be
# entered without a daemon behind it.
#
# As in signal-daemon.sh, callers in tmux hooks must NOT pass #{session_id}:
# tmux expands it to strings like "$5" which bash reads as a positional
# parameter. Pass nothing and let the lookup below resolve it.
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Both args are optional; the plugin's fast path passes them so this stays a
# single fork on the init hot path.
SESSION="${1:-}"
SESSION_NAME="${2:-}"
# Optional third arg: the client tty this hook fired for. Under grouped
# sessions a bare `display-message -p '#{session_id}'` answers with the newest
# session in the group rather than the one the client is actually on, so when a
# tty is known resolve through list-clients, which evaluates the format against
# each client's own session.
CLIENT_TTY="${3:-}"
if { [ -z "$SESSION" ] || [ -z "$SESSION_NAME" ]; } && [ -n "$CLIENT_TTY" ]; then
    _ROW=$(tmux list-clients -F '#{client_tty}|#{session_id}|#{session_name}' 2>/dev/null \
        | grep -F "${CLIENT_TTY}|" | head -1)
    if [ -n "$_ROW" ]; then
        _REST="${_ROW#*|}"
        [ -z "$SESSION" ] && SESSION="${_REST%%|*}"
        [ -z "$SESSION_NAME" ] && SESSION_NAME="${_REST#*|}"
    fi
fi
if [ -z "$SESSION" ] || [ -z "$SESSION_NAME" ]; then
    _INFO=$(tmux display-message -p '#{session_id}|#{session_name}' 2>/dev/null || echo "|")
    [ -z "$SESSION" ] && SESSION="${_INFO%%|*}"
    [ -z "$SESSION_NAME" ] && SESSION_NAME="${_INFO#*|}"
fi
[ -z "$SESSION" ] && exit 0

# Internal holding/stash sessions never get a daemon of their own; one scoped
# there renders a sidebar showing only the parked windows.
case "$SESSION_NAME" in
    _tabby_*) exit 0 ;;
esac

MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
[ "$MODE" = "enabled" ] || [ -z "$MODE" ] || exit 0

SOCK="/tmp/tabby-daemon-${SESSION}.sock"
PIDF="/tmp/tabby-daemon-${SESSION}.pid"
WDF="/tmp/tabby-daemon-${SESSION}.watchdog.pid"

if [ -S "$SOCK" ] && [ -f "$PIDF" ]; then
    PID=$(cat "$PIDF" 2>/dev/null || echo "")
    [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null && exit 0
fi

if [ -f "$WDF" ]; then
    WP=$(cat "$WDF" 2>/dev/null || echo "")
    [ -n "$WP" ] && kill -0 "$WP" 2>/dev/null && exit 0
fi

rm -f "$SOCK" "$PIDF"
"$CURRENT_DIR/bin/tabby" watchdog -session "$SESSION" &
