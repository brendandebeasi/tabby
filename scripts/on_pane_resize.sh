#!/bin/sh
# Called from tmux after-resize-pane hook. Only signals tabby-daemon (USR1)
# if the resized pane is actually a sidebar — prevents content-pane reflows
# (e.g. launching TUI apps like kilo/opencode) from triggering a width-sync
# feedback loop.
set -e
hook_pane="$1"
printf '%s ON_PANE_RESIZE hook_pane=%s\n' "$(date +%H:%M:%S.%3N)" "$hook_pane" >> /tmp/tabby-resize-trace.log 2>/dev/null || true
[ -n "$hook_pane" ] || exit 0
cmd=$(tmux display -p -t "$hook_pane" '#{pane_current_command}' 2>/dev/null || true)
case "$cmd" in
  sidebar*) ;;
  *) exit 0 ;;
esac
pid=$(tmux show-option -gqv @tabby_daemon_pid 2>/dev/null || true)
[ -n "$pid" ] || exit 0
kill -USR1 "$pid" 2>/dev/null || true
