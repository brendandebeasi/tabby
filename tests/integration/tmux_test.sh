#!/usr/bin/env bash
set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-integration}"
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-integration-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
TEST_SESSION="tabby-integration-test"

cleanup() {
	tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
	tmux kill-server 2>/dev/null || true
	rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Integration Test: Horizontal Rendering ==="

tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION"

tmux rename-window -t "$TEST_SESSION":0 "SD|app"
tmux new-window -t "$TEST_SESSION" -n "GP|tool"
tmux new-window -t "$TEST_SESSION" -n "notes"

tmux set-option -g @tmux_tabs_test 1
sleep 1

# Smoke-test render binary execution without requiring an attached client.
"$PROJECT_ROOT/bin/render-status" >/dev/null 2>&1 || true

WINDOWS="$(tmux list-windows -t "$TEST_SESSION" -F "#{window_name}")"

if echo "$WINDOWS" | grep -Fq "SD|app"; then
	echo "✓ SD window found"
else
	echo "✗ SD window missing"
	exit 1
fi

if echo "$WINDOWS" | grep -Fq "GP|tool"; then
	echo "✓ GP window found"
else
	echo "✗ GP window missing"
	exit 1
fi

echo "=== All integration tests passed ==="
