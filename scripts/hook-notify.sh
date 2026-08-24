#!/usr/bin/env bash
# hook-notify.sh
#
# The standard tmux-hook body: signal this session's daemon (USR1) so it
# reconciles state, then repaint the client.
#
# This exists to make a hook body that CANNOT report failure. tmux prints
# "'<body>' returned N" into every attached client when a run-shell body exits
# nonzero, and these hooks fire on every window switch, link, unlink and split
# — so one transient error becomes a screenful of noise that redraws over the
# user's work. The old bodies chained the steps inline and ended in `true`,
# which covers a step that *fails* but not a shell that dies before reaching
# the end; `returned 1` kept appearing in the wild that no direct replay of the
# same string could reproduce. Ending this script with an unconditional
# `exit 0` removes the whole class of report, whatever the steps did.
#
# Nothing consumes a hook's exit status — these steps are best-effort
# housekeeping and the daemon reconciles on its own schedule regardless — so a
# failure is logged rather than propagated. See TABBY_HOOK_LOG.
#
# Keep this body to steps that need no arguments. tmux expands #{session_id}
# to text like `$246`, which a shell then reads as a positional parameter and
# silently substitutes away, so anything taking a session id must be invoked
# from its own run-shell with the format single-quoted at the shell level.

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

"$(dirname "$0")/signal-daemon.sh" >/dev/null 2>&1 || note "$?" signal-daemon

# --no-refresh is for hooks that fire while the layout is already settling
# (pane select, split, kill). The daemon broadcasts its own render once it
# handles the signal, and repainting ahead of that just makes the sidebar
# flicker.
if [ "${1:-}" != "--no-refresh" ]; then
    # refresh-client needs a current client, and a backgrounded run-shell fired
    # by a hook whose originating client has already gone away has none. That
    # miss is expected, so it is silenced rather than logged.
    tmux refresh-client -S >/dev/null 2>&1 || true
fi

exit 0
