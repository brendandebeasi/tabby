#!/usr/bin/env bash
set -euo pipefail

TABBY_DIR="$(cd "$(dirname "$0")/../.." >/dev/null && pwd -P)"
SESSION="tabby-overview-test-$$"
PASS=0
FAIL=0

cleanup() {
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    pkill -f "tabby-daemon.*$SESSION" 2>/dev/null || true
    rm -f "/tmp/tabby-daemon-$SESSION.pid" "/tmp/tabby-daemon-$SESSION.sock" 2>/dev/null || true
}
trap cleanup EXIT

pass() { echo "PASS: $1"; ((PASS++)); }
fail() { echo "FAIL: $1"; ((FAIL++)); }

echo "Building tabby-daemon..."
cd "$TABBY_DIR"
go build -o bin/tabby-daemon ./cmd/tabby-daemon 2>&1 || { echo "BUILD FAILED"; exit 1; }

tmux new-session -d -s "$SESSION" -x 200 -y 50
tmux new-window -d -t "$SESSION"
tmux new-window -d -t "$SESSION"

DEFAULT_MODE=$(tmux show-option -gqv @tabby_view_mode 2>/dev/null || echo "")
[[ "$DEFAULT_MODE" == "" || "$DEFAULT_MODE" == "current" ]] && pass "default view mode is current/empty" || fail "default view mode unexpected: $DEFAULT_MODE"

tmux set-option -g @tabby_view_mode "overview"
MODE=$(tmux show-option -gqv @tabby_view_mode)
[[ "$MODE" == "overview" ]] && pass "set to overview mode" || fail "failed to set overview mode: $MODE"

tmux set-option -g @tabby_view_mode "current"
MODE=$(tmux show-option -gqv @tabby_view_mode)
[[ "$MODE" == "current" ]] && pass "set back to current mode" || fail "failed to reset to current: $MODE"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]] && echo "PASS" && exit 0 || echo "FAIL" && exit 1
