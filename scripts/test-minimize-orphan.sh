#!/usr/bin/env bash
# test-minimize-orphan.sh
#
# Live end-to-end repro/verification for the window-minimize orphan bug and
# its fix: reconcileOrphanedMinimizedWindows (cmd/tabby/internal/daemon/coordinator.go),
# called from NewCoordinator at daemon startup.
#
# Bug: a window parked in the "_tabby_minimized" holding session with its
# @tabby_min_origin/@tabby_minimized/@tabby_min_dir/@tabby_min_host markers
# cleared (a crash mid-unpark) or pointing at a session that no longer exists
# becomes permanently invisible: the sidebar only lists a parked window when
# @tabby_min_origin equals the current session id, and there is no UI path
# back into an untagged/misrouted window.
#
# This script builds tabby fresh (to /tmp, NEVER to bin/tabby) and drives a
# real daemon against an ISOLATED tmux server (`tmux -L tabby-minimize-test`)
# so it never touches the user's live tmux/tabby session.
#
# MUST run on the tabby-dev OrbStack VM (Linux tmux + /usr/local/go), not on
# the Mac host. Two ways to run it:
#   1. scripts/vm-test.sh scripts/test-minimize-orphan.sh
#   2. orb -m tabby-dev bash -lc "cd /Users/b/git/tabby && bash scripts/test-minimize-orphan.sh"
#
# Exit code is non-zero if any scenario assertion fails.

set -uo pipefail

# ── config ──────────────────────────────────────────────────────────────────

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-minimize-test}"
RUNTIME_PREFIX="${TABBY_TEST_RUNTIME_PREFIX:-minimize-orphan-test-}"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN="/tmp/tabby-minimize-orphan-test-bin"
WRAP_DIR="/tmp/tabby-minimize-orphan-test-tmux-wrap"
USER_SESSION="mo-main"
OTHER_SESSION="mo-other"

export TABBY_RUNTIME_PREFIX="$RUNTIME_PREFIX"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

log_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
log_pass() { echo -e "${GREEN}[PASS]${NC} $1"; TESTS_PASSED=$((TESTS_PASSED + 1)); TESTS_RUN=$((TESTS_RUN + 1)); }
log_fail() { echo -e "${RED}[FAIL]${NC} $1"; TESTS_FAILED=$((TESTS_FAILED + 1)); TESTS_RUN=$((TESTS_RUN + 1)); }

assert_eq() {
	# assert_eq <label> <actual> <expected>
	local label="$1" actual="$2" expected="$3"
	if [ "$actual" = "$expected" ]; then
		log_pass "$label (got '$actual')"
	else
		log_fail "$label (got '$actual', expected '$expected')"
	fi
}

assert_empty() {
	local label="$1" actual="$2"
	if [ -z "$actual" ]; then
		log_pass "$label (empty, as expected)"
	else
		log_fail "$label (expected empty, got '$actual')"
	fi
}

# ── isolated tmux ────────────────────────────────────────────────────────────
# Shadow the `tmux` binary on PATH with one that always adds
# `-L $TABBY_TEST_SOCKET -f /dev/null`, exactly like tests/e2e/test_utils.sh,
# so every `tmux ...` call below (and every exec.Command("tmux", ...) inside
# the daemon we launch, since it inherits this PATH) targets our throwaway
# server, never the user's real tmux.
mkdir -p "$WRAP_DIR"
REAL_TMUX="$(command -v tmux)"
cat >"$WRAP_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$REAL_TMUX" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$WRAP_DIR/tmux"
export PATH="$WRAP_DIR:$PATH"

CLIENT_PID=""
DAEMON_PID=""

cleanup() {
	local rc=$?
	log_info "cleaning up (daemon_pid=${DAEMON_PID:-none} client_pid=${CLIENT_PID:-none})"
	[ -n "$DAEMON_PID" ] && kill -TERM "$DAEMON_PID" 2>/dev/null
	[ -n "$CLIENT_PID" ] && kill -TERM "$CLIENT_PID" 2>/dev/null
	sleep 0.3
	tmux kill-server 2>/dev/null
	rm -f /tmp/${RUNTIME_PREFIX}tabby-daemon-*
	rm -f /tmp/tabby-minimize-orphan-test-client.log /tmp/tabby-minimize-orphan-test-client.out
	rm -rf "$WRAP_DIR"
	exit $rc
}
trap cleanup EXIT INT TERM

# ── daemon lifecycle ─────────────────────────────────────────────────────────

SESSION_ID=""     # tmux session id ($N) of USER_SESSION, set by setup_harness
SOCK_PATH=""      # daemon unix socket path, set by start_daemon

sock_path_for() { echo "/tmp/${RUNTIME_PREFIX}tabby-daemon-$1.sock"; }
pid_path_for() { echo "/tmp/${RUNTIME_PREFIX}tabby-daemon-$1.pid"; }

start_daemon() {
	local go_path="/usr/local/go/bin"
	local debug_flag=""
	[ "${TABBY_TEST_DEBUG:-0}" = "1" ] && debug_flag="-debug"
	PATH="$go_path:$PATH" "$BIN" daemon -session "$SESSION_ID" $debug_flag \
		>/tmp/tabby-minimize-orphan-test-daemon.log 2>&1 &
	DAEMON_PID=$!
	SOCK_PATH="$(sock_path_for "$SESSION_ID")"
	local waited=0
	while [ ! -S "$SOCK_PATH" ] && [ $waited -lt 100 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	if [ ! -S "$SOCK_PATH" ]; then
		log_fail "daemon socket never appeared at $SOCK_PATH"
		tail -n 40 /tmp/tabby-minimize-orphan-test-daemon.log
		return 1
	fi
	# Let the coordinator's initial refresh/reconcile settle.
	sleep 1
	return 0
}

stop_daemon() {
	[ -z "$DAEMON_PID" ] && return 0
	kill -TERM "$DAEMON_PID" 2>/dev/null
	local waited=0
	while kill -0 "$DAEMON_PID" 2>/dev/null && [ $waited -lt 50 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	if kill -0 "$DAEMON_PID" 2>/dev/null; then
		log_fail "daemon pid $DAEMON_PID did not exit after SIGTERM; force-killing"
		kill -KILL "$DAEMON_PID" 2>/dev/null
	fi
	DAEMON_PID=""
}

restart_daemon() {
	stop_daemon
	start_daemon
}

# Send one MsgInput over the daemon socket, mimicking a sidebar click, the
# same wire path the UI uses (pkg/daemon/protocol.go InputPayload).
send_action() {
	local action="$1" target="$2"
	local msg
	msg=$(printf '{"type":"input","target":{"kind":"hook","instance":"test"},"payload":{"type":"action","resolved_action":"%s","resolved_target":"%s"}}' "$action" "$target")
	printf '%s\n' "$msg" | nc -U -q1 "$SOCK_PATH"
	sleep 1
}

wopt() {
	# wopt <window_id> <option> -> value (empty if unset/window gone)
	tmux show-window-option -v -t "$1" "$2" 2>/dev/null
}

window_session() {
	tmux display-message -p -t "$1" '#{session_name}' 2>/dev/null
}

window_exists() {
	tmux display-message -p -t "$1" '#{window_id}' >/dev/null 2>&1
}

new_test_window() {
	# new_test_window <name> <session> -> prints the new window_id
	tmux new-window -t "$2" -n "$1" -P -F '#{window_id}'
}

# ── harness bring-up (step 1) ────────────────────────────────────────────────

setup_harness() {
	log_info "tearing down any stale isolated tmux server ($TABBY_TEST_SOCKET)"
	tmux kill-server 2>/dev/null
	rm -f /tmp/${RUNTIME_PREFIX}tabby-daemon-*

	log_info "building tabby -> $BIN"
	if ! (cd "$REPO_ROOT" && PATH="/usr/local/go/bin:$PATH" go build -o "$BIN" ./cmd/tabby); then
		log_fail "go build failed"
		exit 1
	fi

	log_info "starting isolated tmux session $USER_SESSION"
	# Width must stay >=100: computeProfile() classifies <100 as "phone" and the
	# daemon ALWAYS fires one profile-transition on initial boot
	# (maybeScheduleProfileTransition treats prevWidth==0 as "phone" no matter
	# what), which stashes every single-pane window's content into
	# _tabby_limbo/_tabby_content_* if it thinks the active client is
	# phone-narrow. That check reads the ATTACHED CLIENT's real tty size
	# (activeClientGeometry/#{client_width}), not the session's -x/-y or
	# window-size option - so the client we keep attached below must itself
	# have a wide pty, not just a wide tmux window.
	tmux set-option -g window-size manual
	tmux new-session -d -s "$USER_SESSION" -x 220 -y 50
	# Suppress sidebar/renderer spawning noise (extra panes, stash/limbo
	# windows) so window ids stay exactly what this script creates -
	# irrelevant to the minimize/reconcile logic under test either way.
	tmux set-option -g @tabby_spawning 1
	SESSION_ID="$(tmux display-message -p -t "$USER_SESSION" '#{session_id}')"
	log_info "session id: $SESSION_ID"

	# Keep a real tmux client attached for the whole run so the daemon's 30s
	# idle-quit never fires (server.ClientCount()==0 && tmuxAttachedClients==0
	# is the only idle-quit condition - a real attached client defeats it
	# even with zero daemon-socket render clients). A plain `script`-allocated
	# pty inherits whatever (often 80x24, or 0x0 headless) size its own
	# controlling terminal has, which is what fed the "phone" misclassification
	# above - so open the pty ourselves via python3 and set its winsize to
	# 220x50 explicitly before exec'ing `tmux attach` into it.
	python3 - "$USER_SESSION" /tmp/tabby-minimize-orphan-test-client.log <<'PYEOF' &
import os, pty, struct, fcntl, termios, signal, sys

session, logpath = sys.argv[1], sys.argv[2]
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 50, 220, 0, 0))

pid = os.fork()
if pid == 0:
    os.setsid()
    os.dup2(slave, 0)
    os.dup2(slave, 1)
    os.dup2(slave, 2)
    os.close(master)
    os.close(slave)
    os.execvp("tmux", ["tmux", "attach", "-t", session])
    os._exit(1)

os.close(slave)

def handler(signum, frame):
    try:
        os.kill(pid, signal.SIGTERM)
    except OSError:
        pass
    sys.exit(0)

signal.signal(signal.SIGTERM, handler)
signal.signal(signal.SIGINT, handler)

with open(logpath, "wb") as logf:
    while True:
        try:
            data = os.read(master, 4096)
        except OSError:
            break
        if not data:
            break
        logf.write(data)
        logf.flush()
PYEOF
	CLIENT_PID=$!
	sleep 1
	if ! tmux list-clients -t "$USER_SESSION" 2>/dev/null | grep -q attached; then
		log_fail "harness: could not attach a real tmux client (idle-quit will fire)"
		return 1
	fi
	log_pass "harness: isolated tmux session + attached client up (width=$(tmux display-message -p -t "$USER_SESSION" '#{client_width}'))"

	log_info "starting daemon for session $SESSION_ID"
	if ! start_daemon; then
		log_fail "harness: daemon failed to start"
		return 1
	fi
	log_pass "harness: daemon socket up at $SOCK_PATH"
	return 0
}

# ── scenarios ────────────────────────────────────────────────────────────────

scenario_e_round_trip() {
	log_info "--- Scenario E: normal minimize/un-minimize round trip ---"
	local win
	win=$(new_test_window "sc-e" "$USER_SESSION")
	sleep 1

	send_action "toggle_minimize_window" "$win"
	assert_eq "E: window parked into holding session" "$(window_session "$win")" "_tabby_minimized"
	assert_eq "E: @tabby_minimized set" "$(wopt "$win" @tabby_minimized)" "1"
	assert_eq "E: @tabby_min_origin set to live session" "$(wopt "$win" @tabby_min_origin)" "$SESSION_ID"

	send_action "toggle_minimize_window" "$win"
	assert_eq "E: window restored to user session" "$(window_session "$win")" "$USER_SESSION"
	assert_empty "E: @tabby_minimized cleared" "$(wopt "$win" @tabby_minimized)"
	assert_empty "E: @tabby_min_origin cleared" "$(wopt "$win" @tabby_min_origin)"
	assert_empty "E: @tabby_min_dir cleared" "$(wopt "$win" @tabby_min_dir)"
	assert_empty "E: @tabby_min_host cleared" "$(wopt "$win" @tabby_min_host)"

	if tmux has-session -t _tabby_minimized 2>/dev/null; then
		log_fail "E: _tabby_minimized session should be cleaned up once empty"
	else
		log_pass "E: _tabby_minimized session cleaned up once empty"
	fi
}

WIN_A=""
WIN_B=""
WIN_C=""
OTHER_SESSION_ID=""
MAIN_BASELINE=""
FOUNDING_PLACEHOLDER_ID=""

prepare_scenario_a() {
	log_info "--- Scenario A prep: untagged orphan ---"
	WIN_A=$(new_test_window "sc-a" "$USER_SESSION")
	sleep 1
	send_action "toggle_minimize_window" "$WIN_A"
	if [ "$(window_session "$WIN_A")" != "_tabby_minimized" ]; then
		log_fail "A prep: window did not park"
		return 1
	fi
	# Simulate the exact observed bug: a prior daemon cleared the markers
	# (e.g. mid-unpark crash) before the window made it out of the holding
	# session, leaving it stranded and completely untagged.
	tmux set-window-option -u -t "$WIN_A" @tabby_minimized
	tmux set-window-option -u -t "$WIN_A" @tabby_min_origin
	tmux set-window-option -u -t "$WIN_A" @tabby_min_dir
	tmux set-window-option -u -t "$WIN_A" @tabby_min_host
	log_pass "A prep: $WIN_A parked then stranded untagged in _tabby_minimized"
}

prepare_scenario_d2() {
	log_info "--- Scenario D2 prep: legacy (pre-upgrade) holding session, no placeholder tag ---"
	# ensureMinimizedSession tagged the founding window when prepare_scenario_a
	# first created the holding session. Strip that tag to simulate a session
	# founded by a pre-upgrade daemon, shape-identical to a stranded orphan:
	# same as WIN_A, no @tabby_min_origin and no @tabby_min_placeholder.
	FOUNDING_PLACEHOLDER_ID="$(tmux list-windows -t _tabby_minimized -F '#{window_id} #{@tabby_min_placeholder}' | awk '$2 == "1" { print $1; exit }')"
	if [ -z "$FOUNDING_PLACEHOLDER_ID" ]; then
		log_fail "D2 prep: could not find the ensureMinimizedSession placeholder window"
		return 1
	fi
	tmux set-window-option -u -t "$FOUNDING_PLACEHOLDER_ID" @tabby_min_placeholder
	log_pass "D2 prep: stripped @tabby_min_placeholder from founding window $FOUNDING_PLACEHOLDER_ID"
}

prepare_scenario_b() {
	log_info "--- Scenario B prep: dead origin session ---"
	WIN_B=$(new_test_window "sc-b" "$USER_SESSION")
	sleep 1
	send_action "toggle_minimize_window" "$WIN_B"
	if [ "$(window_session "$WIN_B")" != "_tabby_minimized" ]; then
		log_fail "B prep: window did not park"
		return 1
	fi
	tmux set-window-option -t "$WIN_B" @tabby_min_origin '$999'
	log_pass "B prep: $WIN_B retagged to dead session \$999"
}

prepare_scenario_c() {
	log_info "--- Scenario C prep: window tagged to a foreign LIVE session ---"
	tmux new-session -d -s "$OTHER_SESSION" -x 220 -y 50
	OTHER_SESSION_ID="$(tmux display-message -p -t "$OTHER_SESSION" '#{session_id}')"
	# Build the parked window directly (not through THIS daemon, which would
	# only ever tag windows with its own session id) to model a window a
	# DIFFERENT, still-alive daemon parked for OTHER_SESSION.
	WIN_C=$(new_test_window "sc-c" "$OTHER_SESSION")
	tmux move-window -d -s "$WIN_C" -t "_tabby_minimized:"
	tmux set-window-option -t "$WIN_C" @tabby_minimized 1
	tmux set-window-option -t "$WIN_C" @tabby_min_origin "$OTHER_SESSION_ID"
	log_pass "A/B/C prep: $WIN_C parked and tagged to live session $OTHER_SESSION_ID"
}

capture_main_baseline() {
	# Every window presently in USER_SESSION, before the restart that should
	# rehome WIN_A back into it (scenario D's "no junk tab" check compares
	# against baseline + WIN_A, nothing else).
	MAIN_BASELINE="$(tmux list-windows -t "$USER_SESSION" -F '#{window_id}' | sort)"
}

assert_scenario_a() {
	log_info "--- Scenario A assert: untagged orphan rehomed + fully unminimized ---"
	assert_eq "A: window rehomed into user session" "$(window_session "$WIN_A")" "$USER_SESSION"
	assert_empty "A: @tabby_minimized cleared" "$(wopt "$WIN_A" @tabby_minimized)"
	assert_empty "A: @tabby_min_origin cleared" "$(wopt "$WIN_A" @tabby_min_origin)"
	assert_empty "A: @tabby_min_dir cleared" "$(wopt "$WIN_A" @tabby_min_dir)"
	assert_empty "A: @tabby_min_host cleared" "$(wopt "$WIN_A" @tabby_min_host)"
}

assert_scenario_b() {
	log_info "--- Scenario B assert: dead-origin window adopted, still parked ---"
	assert_eq "B: window still parked in holding session" "$(window_session "$WIN_B")" "_tabby_minimized"
	assert_eq "B: @tabby_min_origin adopted to live session" "$(wopt "$WIN_B" @tabby_min_origin)" "$SESSION_ID"
	assert_eq "B: @tabby_minimized still set" "$(wopt "$WIN_B" @tabby_minimized)" "1"
}

assert_scenario_c() {
	log_info "--- Scenario C assert: foreign-live-session window left untouched ---"
	assert_eq "C: window still parked in holding session" "$(window_session "$WIN_C")" "_tabby_minimized"
	assert_eq "C: @tabby_min_origin unchanged" "$(wopt "$WIN_C" @tabby_min_origin)" "$OTHER_SESSION_ID"
	assert_eq "C: @tabby_minimized unchanged" "$(wopt "$WIN_C" @tabby_minimized)" "1"
}

assert_scenario_d2() {
	log_info "--- Scenario D2 assert: legacy untagged placeholder skipped + retagged, not rehomed ---"
	assert_eq "D2: legacy placeholder window still parked in holding session" "$(window_session "$FOUNDING_PLACEHOLDER_ID")" "_tabby_minimized"
	assert_eq "D2: legacy placeholder window retagged" "$(wopt "$FOUNDING_PLACEHOLDER_ID" @tabby_min_placeholder)" "1"
}

assert_scenario_d() {
	log_info "--- Scenario D assert: placeholder window never dragged into user session ---"
	local expected after
	expected="$(printf '%s\n%s' "$MAIN_BASELINE" "$WIN_A" | sort)"
	after="$(tmux list-windows -t "$USER_SESSION" -F '#{window_id}' | sort)"
	assert_eq "D: user session window set is baseline+rehomed-orphan only" "$after" "$expected"
	if echo "$after" | grep -qx "$WIN_B\|$WIN_C"; then
		log_fail "D: a still-parked window leaked into the user session"
	else
		log_pass "D: no still-parked window leaked into the user session"
	fi
}

run_reconciliation_scenarios() {
	prepare_scenario_a || return 1
	prepare_scenario_d2 || return 1
	prepare_scenario_b || return 1
	prepare_scenario_c || return 1
	capture_main_baseline

	log_info "restarting daemon to trigger reconcileOrphanedMinimizedWindows"
	if ! restart_daemon; then
		log_fail "daemon restart failed; skipping A/B/C/D/D2 assertions"
		return 1
	fi

	assert_scenario_a
	assert_scenario_b
	assert_scenario_c
	assert_scenario_d
	assert_scenario_d2
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
	setup_harness || { echo "harness bring-up failed, aborting"; exit 1; }

	scenario_e_round_trip
	if [ "${TABBY_TEST_SKIP_RECONCILE:-0}" = "1" ]; then
		log_info "TABBY_TEST_SKIP_RECONCILE=1: skipping A/B/C/D (harness dry-run only)"
	else
		run_reconciliation_scenarios
	fi

	echo ""
	echo "==================================================================="
	echo "Results: $TESTS_PASSED/$TESTS_RUN passed, $TESTS_FAILED failed"
	echo "==================================================================="
	[ "$TESTS_FAILED" -eq 0 ]
}

main
