#!/usr/bin/env bash
# tabby-watch.sh — one-shot health scan of a live tabby runtime.
#
# Prints one line per check: "OK <name>" or "FINDING <name>: <detail>",
# then a summary. Exit 1 when any FINDING was printed, so a watcher loop or
# an agent can react. Read-only except where a check explicitly fixes state
# (none today). Every external call is local and fast; lsof is the slow one
# (~2s). Safe to run on repeat.
#
# Checks encode the failure modes seen in the wild:
#   stale-binary    daemon running an older build than bin/tabby (e2e and
#                   capture scripts rebuild bin/tabby under a running daemon)
#   dead-renderer   renderer process with no socket to its daemon — the
#                   frozen-sidebar symptom; pre-Kill-escalation these also
#                   ignored SIGTERM
#   restart-storm   daemon killed and watchdog-restarted repeatedly
#   owner-flap      grouped-session layout ownership changing hands fast
#                   (the judder signal); handoffs are logged since the
#                   hysteresis fix
#   orphan-daemon   daemon alive for a session tmux no longer has
#   stale-plugin    ~/.tmux/plugins/tabby a real dir drifting from the repo
#   log-bloat       events/stderr logs growing past a healthy size

set -u
REPO="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
FINDINGS=0

ok()      { echo "OK $1"; }
finding() { echo "FINDING $1: $2"; FINDINGS=$((FINDINGS + 1)); }

now_epoch() { date +%s; }
# "2026/08/24 15:13:50" -> epoch (macOS date)
line_epoch() { date -j -f '%Y/%m/%d %H:%M:%S' "$1" +%s 2>/dev/null || echo 0; }

# --- stale-binary ------------------------------------------------------------
STATUS_OUT=$("$REPO/bin/tabby" dev status 2>&1)
if echo "$STATUS_OUT" | grep -q 'STALE'; then
	finding stale-binary "$(echo "$STATUS_OUT" | grep -m1 'fix:')"
else
	ok stale-binary
fi

# --- dead-renderer -----------------------------------------------------------
# A healthy renderer (sidebar / pane-header / window-header) holds one
# connected unix socket to its daemon. None = wedged, frozen on screen.
LSOF=$(lsof -U 2>/dev/null)
RENDERER_PIDS=$(pgrep -x sidebar-renderer; pgrep -x pane-header; pgrep -x window-header)
DEAD=""
for pid in $RENDERER_PIDS; do
	if ! echo "$LSOF" | awk -v p="$pid" '$2 == p && $NF ~ /^->/ {found=1} END {exit !found}'; then
		DEAD="$DEAD $pid($(ps -o command= -p "$pid" | awk '{print $4, $6}'))"
	fi
done
if [ -n "$DEAD" ]; then
	finding dead-renderer "no daemon socket:$DEAD — kill -9 and let the daemon respawn"
else
	ok dead-renderer
fi

# --- restart-storm -----------------------------------------------------------
CUTOFF=$(($(now_epoch) - 900))
RESTARTS=0
for f in /tmp/tabby-daemon-*-crash.log; do
	[ -e "$f" ] || continue
	[ "$(stat -f %m "$f")" -lt "$CUTOFF" ] && continue
	while read -r ts _; do
		ep=$(line_epoch "$ts")
		[ "$ep" -ge "$CUTOFF" ] && RESTARTS=$((RESTARTS + 1))
	done < <(grep 'WATCHDOG_RESTART' "$f" | awk '{print $1, $2}')
done
if [ "$RESTARTS" -gt 5 ]; then
	finding restart-storm "$RESTARTS watchdog restarts in 15min"
else
	ok restart-storm
fi

# --- owner-flap --------------------------------------------------------------
FLIPS=0
HANDOFFS=0
for f in /tmp/tabby-daemon-*-events.log; do
	[ -e "$f" ] || continue
	[ "$(stat -f %m "$f")" -lt "$CUTOFF" ] && continue
	while read -r ts rest; do
		ep=$(line_epoch "$ts")
		[ "$ep" -lt "$CUTOFF" ] && continue
		case "$rest" in
			*owns=true*) FLIPS=$((FLIPS + 1)) ;;
			*HANDOFF*)   HANDOFFS=$((HANDOFFS + 1)) ;;
		esac
	done < <(grep -h 'GROUP_LAYOUT_OWNER\|GROUP_LAYOUT_HANDOFF' "$f" | sed 's/\[event\] //' | awk '{print $1, $2, $0}')
done
if [ "$FLIPS" -gt 3 ]; then
	finding owner-flap "$FLIPS ownership elections won in 15min ($HANDOFFS lease handoffs)"
else
	ok owner-flap
fi

# --- orphan-daemon -----------------------------------------------------------
LIVE_SESSIONS=$(tmux list-sessions -F '#{session_id}' 2>/dev/null | tr '\n' ' ')
ORPHANS=""
for p in /tmp/tabby-daemon-*.pid; do
	[ -e "$p" ] || continue
	sess=$(basename "$p" .pid); sess=${sess#tabby-daemon-}
	pid=$(cat "$p" 2>/dev/null)
	[ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || continue
	case " $LIVE_SESSIONS " in
		*" $sess "*) ;;
		*) ORPHANS="$ORPHANS $sess(pid=$pid)" ;;
	esac
done
if [ -n "$ORPHANS" ]; then
	finding orphan-daemon "daemon alive for dead session:$ORPHANS"
else
	ok orphan-daemon
fi

# --- stale-plugin ------------------------------------------------------------
PLUGIN="$HOME/.tmux/plugins/tabby"
if [ -d "$PLUGIN" ] && [ ! -L "$PLUGIN" ] && [ -f "$PLUGIN/bin/tabby" ]; then
	A=$(shasum -a 256 "$PLUGIN/bin/tabby" | awk '{print $1}')
	B=$(shasum -a 256 "$REPO/bin/tabby" | awk '{print $1}')
	if [ "$A" != "$B" ]; then
		finding stale-plugin "$PLUGIN/bin/tabby differs from repo build"
	else
		ok stale-plugin
	fi
else
	ok stale-plugin
fi

# --- log-bloat ---------------------------------------------------------------
BLOATED=""
for f in /tmp/tabby-daemon-*-events.log /tmp/tabby-daemon-*-stderr.log; do
	[ -e "$f" ] || continue
	size=$(stat -f %z "$f")
	[ "$size" -gt 5242880 ] && BLOATED="$BLOATED $(basename "$f")=$((size / 1048576))MB"
done
if [ -n "$BLOATED" ]; then
	finding log-bloat "$BLOATED"
else
	ok log-bloat
fi

# --- wedged-loop -------------------------------------------------------------
# The daemon's main loop heartbeats each iteration; the file heartbeat writer
# (a separate goroutine) keeps beating while the loop is wedged, so a fresh
# file alone proves nothing. loop_age_ms is the real signal.
WEDGED=""
for h in /tmp/tabby-daemon-*.heartbeat; do
	[ -e "$h" ] || continue
	[ "$(stat -f %m "$h")" -lt "$CUTOFF" ] && continue
	age=$(awk -F= '$1 == "loop_age_ms" {print $2}' "$h")
	if [ -n "$age" ] && [ "$age" -gt 30000 ]; then
		WEDGED="$WEDGED $(basename "$h" .heartbeat)(loop_age_ms=$age)"
	fi
done
if [ -n "$WEDGED" ]; then
	finding wedged-loop "daemon loop not dispatching:$WEDGED — it self-terminates after 30s on current builds; older builds need a manual kill"
else
	ok wedged-loop
fi

# --- stale-client ------------------------------------------------------------
# Reads the daemon status snapshots (-status.json): a renderer that has not
# spoken to its daemon in 90s is frozen regardless of what its socket shows.
STALE=""
for f in /tmp/tabby-daemon-*-status.json; do
	[ -e "$f" ] || continue
	[ "$(stat -f %m "$f")" -lt "$CUTOFF" ] && continue
	while read -r id age; do
		[ "$age" -gt 90000 ] && STALE="$STALE ${id}($((age / 1000))s)"
	done < <(python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
for c in d.get("clients", []):
    print(c.get("id", "?"), c.get("last_seen_age_ms", 0))
' "$f" 2>/dev/null)
done
if [ -n "$STALE" ]; then
	finding stale-client "renderer silent >90s:$STALE"
else
	ok stale-client
fi

# --- width-flap --------------------------------------------------------------
# Sidebar-width adopts are rare in a healthy system (user drags). More than 2
# in 15min means the measurement loop is eating mid-reflow widths again.
ADOPTS=0
for f in /tmp/tabby-daemon-*-events.log; do
	[ -e "$f" ] || continue
	[ "$(stat -f %m "$f")" -lt "$CUTOFF" ] && continue
	while read -r ts rest; do
		ep=$(line_epoch "$ts")
		[ "$ep" -ge "$CUTOFF" ] && ADOPTS=$((ADOPTS + 1))
	done < <(grep 'WIDTH_SYNC_ADOPT ' "$f" | sed 's/\[event\] //' | awk '{print $1, $2, $0}')
done
if [ "$ADOPTS" -gt 2 ]; then
	finding width-flap "$ADOPTS sidebar width adopts in 15min"
else
	ok width-flap
fi

# --- summary -----------------------------------------------------------------
if [ "$FINDINGS" -gt 0 ]; then
	echo "SUMMARY: $FINDINGS finding(s)"
	exit 1
fi
echo "SUMMARY: clean"
exit 0
