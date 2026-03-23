#!/usr/bin/env bash
set -euo pipefail

TABBY_DIR="$(cd "$(dirname "$0")/../.." >/dev/null && pwd -P)"
SESSION="tabby-overview-test-$$"
TMUX_SOCKET="tabby-overview-sock-$$"
TMUX_CMD=(tmux -L "$TMUX_SOCKET" -f /dev/null)
PASS=0
FAIL=0

cleanup() {
    "${TMUX_CMD[@]}" kill-session -t "$SESSION" 2>/dev/null || true
    "${TMUX_CMD[@]}" kill-server 2>/dev/null || true
    pkill -f "tabby-daemon.*$SESSION" 2>/dev/null || true
    rm -f "/tmp/tabby-daemon-$SESSION.pid" "/tmp/tabby-daemon-$SESSION.sock" 2>/dev/null || true
}
trap cleanup EXIT

pass() { echo "PASS: $1"; ((PASS+=1)); return 0; }
fail() { echo "FAIL: $1"; ((FAIL+=1)); return 0; }

echo "Building tabby-daemon..."
cd "$TABBY_DIR"
go build -o bin/tabby-daemon ./cmd/tabby-daemon 2>&1 || { echo "BUILD FAILED"; exit 1; }

"${TMUX_CMD[@]}" new-session -d -s "$SESSION" -x 200 -y 50
"${TMUX_CMD[@]}" new-window -d -t "$SESSION"
"${TMUX_CMD[@]}" new-window -d -t "$SESSION"

DEFAULT_MODE=$("${TMUX_CMD[@]}" show-option -gqv @tabby_view_mode 2>/dev/null || echo "")
[[ "$DEFAULT_MODE" == "" || "$DEFAULT_MODE" == "current" ]] && pass "default view mode is current/empty" || fail "default view mode unexpected: $DEFAULT_MODE"

"${TMUX_CMD[@]}" set-option -g @tabby_view_mode "overview"
MODE=$("${TMUX_CMD[@]}" show-option -gqv @tabby_view_mode)
[[ "$MODE" == "overview" ]] && pass "set to overview mode" || fail "failed to set overview mode: $MODE"

"${TMUX_CMD[@]}" set-option -g @tabby_view_mode "current"
MODE=$("${TMUX_CMD[@]}" show-option -gqv @tabby_view_mode)
[[ "$MODE" == "current" ]] && pass "set back to current mode" || fail "failed to reset to current: $MODE"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]] && echo "PASS" && exit 0 || echo "FAIL" && exit 1
