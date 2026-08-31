#!/usr/bin/env bash
# hook-notify.sh [--no-refresh] [session_id] [session_attached]
#
# The standard tmux-hook body: signal this session's daemon (USR1) so it
# reconciles state, then repaint the client.
#
# This exists to make a hook body that CANNOT report failure. tmux prints
# "'<body>' returned N" into every attached client when a run-shell body exits
# nonzero, and these hooks fire on every window switch, link, unlink and split
# — so one transient error becomes a screenful of noise that redraws over the
# user's work. This script ends in an unconditional `exit 0`, but that only
# covers a step inside it failing, not the shell dying before it reaches the
# last line; the tmux-level `if-shell -b '<body>' ''` in tabby.tmux is what
# covers the rest. See the comment above HOOK_OK there.
#
# Nothing consumes a hook's exit status — these steps are best-effort
# housekeeping and the daemon reconciles on its own schedule regardless — so a
# failure is logged rather than propagated. See TABBY_HOOK_LOG.
#
# EVERY tmux CLIENT CALL HERE COSTS THE SERVER AN FD. A hook fires once per
# session a window is linked into, so on an 8-session group a single new-window
# runs this script eight times over — and each `tmux` invocation is a client
# connecting to the server, holding a server fd until it exits. Measured on a
# live 8-session group, one new-window took the server from 41 open fds to 234
# against a soft RLIMIT_NOFILE of 256; past that ceiling socketpair() fails
# inside job_run() and tmux prints "failed to run command: <body>", which no
# hook body can suppress because the job never exists. So the session id and
# the attached-client count are passed IN as tmux formats — which the server
# expands for free while dispatching the hook — rather than looked up here with
# two more client calls.
#
# tmux expands #{session_id} to text like `$246`, which a shell then reads as a
# positional parameter and silently substitutes away, so callers must
# single-quote the format AT THE SHELL LEVEL. Double quotes do not help.

no_refresh=0
if [ "${1:-}" = "--no-refresh" ]; then
    no_refresh=1
    shift
fi
session="${1:-}"
attached="${2:-}"

log_file="${TABBY_HOOK_LOG:-/tmp/tabby-hook-errors.log}"

# note records a failed step. Hooks can fire hundreds of times a minute during
# a window-heavy operation, so the log is truncated once it passes ~256KB
# rather than growing without bound.
note() {
    if [ -f "$log_file" ]; then
        size=$(wc -c <"$log_file" 2>/dev/null || echo 0)
        [ "${size:-0}" -gt 262144 ] && : >"$log_file"
    fi
    printf '%s pid=%s status=%s step=%s\n' \
        "$(date '+%Y-%m-%d %H:%M:%S')" "$$" "$1" "$2" >>"$log_file" 2>&1
}

# Signal inline rather than exec'ing signal-daemon.sh: same three lines, one
# fewer process per fire. signal-daemon.sh stays for its own callers (the mouse
# bindings), which have no session id to hand and do pay for the lookup.
if [ -z "$session" ]; then
    session=$(tmux display-message -p '#{session_id}' 2>/dev/null || true)
fi
if [ -n "$session" ]; then
    pid=$(cat "/tmp/${TABBY_RUNTIME_PREFIX}tabby-daemon-${session}.pid" 2>/dev/null)
    if [ -n "$pid" ]; then
        kill -USR1 "$pid" 2>/dev/null || note "$?" signal-daemon
    fi
fi

# --no-refresh is for hooks that fire while the layout is already settling
# (pane select, split, kill). The daemon broadcasts its own render once it
# handles the signal, and repainting ahead of that just makes the sidebar
# flicker.
#
# A session with no attached client has nothing to repaint, and refresh-client
# needs a current client anyway — a backgrounded hook whose originating client
# has gone away has none. That call was always a silent no-op on those, so when
# the caller tells us the count is 0 we skip it and save the connection. An
# absent count means an old caller, so keep the old behaviour and try.
if [ "$no_refresh" -eq 0 ] && [ "$attached" != "0" ]; then
    tmux refresh-client -S >/dev/null 2>&1 || true
fi

exit 0
