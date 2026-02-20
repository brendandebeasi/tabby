#!/usr/bin/env bash
set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-orphan-cleanup}"
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-orphan-cleanup-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
TEST_SESSION="tabby-orphan-cleanup-test"

cleanup() {
  tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true
  rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Integration Test: Orphan Window Cleanup ==="

tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION" -n "main"
tmux set-option -g @tabby_test 1

SESSION_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{session_id}')"

create_orphan_window() {
  local name="$1"
  tmux new-window -d -t "$TEST_SESSION" -n "$name" "sleep 120"
  tmux split-window -d -h -t "$TEST_SESSION:$name" "exec -a sidebar sleep 120"
  sleep 0.2
}

kill_non_system_pane() {
  local window_name="$1"
  local pane_id
  pane_id="$(tmux list-panes -t "$TEST_SESSION:$window_name" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' | awk -F'|' '$2 !~ /(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)/ && $3 !~ /(sidebar|sidebar-renderer|tabbar|pane-bar|pane-header)/ { print $1; exit }')"
  if [ -z "$pane_id" ]; then
    echo "✗ No non-system pane found in $window_name" >&2
    exit 1
  fi
  tmux kill-pane -t "$pane_id"
}

window_exists_by_id() {
  local wid="$1"
  tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | grep -qx "$wid"
}

create_orphan_window "orphan-a"
WINDOW_A_ID="$(tmux display-message -p -t "$TEST_SESSION:orphan-a" '#{window_id}')"
kill_non_system_pane "orphan-a"

bash "$PROJECT_ROOT/scripts/cleanup_orphan_sidebar.sh" "$SESSION_ID" "$WINDOW_A_ID"
sleep 0.2

if window_exists_by_id "$WINDOW_A_ID"; then
  echo "✗ Orphan window not closed when targeted directly" >&2
  exit 1
fi
echo "✓ Targeted orphan window closed"

create_orphan_window "orphan-b"
create_orphan_window "orphan-c"
WINDOW_B_ID="$(tmux display-message -p -t "$TEST_SESSION:orphan-b" '#{window_id}')"
WINDOW_C_ID="$(tmux display-message -p -t "$TEST_SESSION:orphan-c" '#{window_id}')"
kill_non_system_pane "orphan-b"
kill_non_system_pane "orphan-c"

bash "$PROJECT_ROOT/scripts/cleanup_orphan_sidebar.sh" "$SESSION_ID" "$WINDOW_B_ID"
sleep 0.2

if window_exists_by_id "$WINDOW_B_ID"; then
  echo "✗ Target orphan window still exists after session sweep" >&2
  exit 1
fi
if window_exists_by_id "$WINDOW_C_ID"; then
  echo "✗ Secondary orphan window still exists after session sweep" >&2
  exit 1
fi

echo "✓ Session-wide orphan cleanup removed multiple orphan windows"
echo "=== Orphan window cleanup test passed ==="
