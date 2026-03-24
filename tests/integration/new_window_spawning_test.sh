#!/usr/bin/env bash
set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-new-window}"
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-new-window-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
TEST_SESSION="tabby-new-window-test"

PASS=0
FAIL=0

cleanup() {
  tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true
  rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}
trap cleanup EXIT

pass() { echo "✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "✗ $1" >&2; FAIL=$((FAIL + 1)); }

echo "=== Integration Test: New Window Spawning ==="

tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION" -n "main"

SESSION_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{session_id}')"

count_windows() {
  tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | wc -l | tr -d ' '
}

get_window_id_by_name() {
  tmux list-windows -t "$TEST_SESSION" -F '#{window_id}|#{window_name}' | awk -F'|' -v name="$1" '$2 == name { print $1; exit }'
}

# ─── Test 1: Script creates window with -d (detached) ───

echo ""
echo "--- Test 1: Window created detached ---"

BEFORE=$(count_windows)
bash "$PROJECT_ROOT/scripts/new_window_with_group.sh" 2>/dev/null
sleep 0.3
AFTER=$(count_windows)

if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
  pass "new_window_with_group.sh created exactly one window"
else
  fail "Expected $((BEFORE + 1)) windows, got $AFTER"
fi

# ─── Test 2: @tabby_spawning is cleared after script completes ───

echo ""
echo "--- Test 2: @tabby_spawning cleared after script ---"

SPAWNING=$(tmux show-option -gqv @tabby_spawning 2>/dev/null || echo "")
if [ -z "$SPAWNING" ] || [ "$SPAWNING" != "1" ]; then
  pass "@tabby_spawning is cleared after script"
else
  fail "@tabby_spawning is still set to 1"
fi

# ─── Test 3: @tabby_new_window_id is set immediately after script ───

echo ""
echo "--- Test 3: @tabby_new_window_id set during window creation ---"

NEW_WIN_ID=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
if [ -n "$NEW_WIN_ID" ]; then
  pass "@tabby_new_window_id is set ($NEW_WIN_ID)"
else
  fail "@tabby_new_window_id is not set"
fi

# ─── Test 4: @tabby_new_window_id cleared after delay ───

echo ""
echo "--- Test 4: @tabby_new_window_id auto-clears after 2s ---"

sleep 2.5
NEW_WIN_ID_AFTER=$(tmux show-option -gqv @tabby_new_window_id 2>/dev/null || echo "")
if [ -z "$NEW_WIN_ID_AFTER" ]; then
  pass "@tabby_new_window_id cleared after delay"
else
  fail "@tabby_new_window_id still set to '$NEW_WIN_ID_AFTER'"
fi

# ─── Test 5: Group inheritance ───

echo ""
echo "--- Test 5: Window inherits group from @tabby_new_window_group ---"

tmux set-option -g @tabby_new_window_group "TestGroup" 2>/dev/null || true
BEFORE=$(count_windows)
bash "$PROJECT_ROOT/scripts/new_window_with_group.sh" 2>/dev/null
sleep 0.3
AFTER=$(count_windows)

if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
  NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
  GROUP=$(tmux show-window-options -t "$NEWEST_WIN" -v @tabby_group 2>/dev/null || echo "")
  if [ "$GROUP" = "TestGroup" ]; then
    pass "New window has group 'TestGroup'"
  else
    fail "New window group is '$GROUP', expected 'TestGroup'"
  fi
else
  fail "Window not created for group test"
fi

SAVED_GROUP_AFTER=$(tmux show-option -gqv @tabby_new_window_group 2>/dev/null || echo "")
if [ -z "$SAVED_GROUP_AFTER" ]; then
  pass "@tabby_new_window_group cleared after use"
else
  fail "@tabby_new_window_group still set to '$SAVED_GROUP_AFTER'"
fi

# ─── Test 6: @tabby_spawning guard check precedes USR1 in ensure_sidebar.sh ───

echo ""
echo "--- Test 6: Spawning flag ordering (cleared before USR1) ---"

# @tabby_spawning is owned by the daemon (Go), not new_window_with_group.sh.
# The guard that prevents signals during spawning lives in ensure_sidebar.sh:
# it reads @tabby_spawning early and bails, and only signals USR1 later.
GUARD_LINE=$(grep -n 'tabby_spawning' "$PROJECT_ROOT/scripts/ensure_sidebar.sh" | head -1 | cut -d: -f1)
USR1_LINE=$(grep -n 'kill -USR1' "$PROJECT_ROOT/scripts/ensure_sidebar.sh" | head -1 | cut -d: -f1)

if [ -n "$GUARD_LINE" ] && [ -n "$USR1_LINE" ] && [ "$GUARD_LINE" -lt "$USR1_LINE" ]; then
  pass "@tabby_spawning guard (line $GUARD_LINE) is before USR1 signal (line $USR1_LINE) in ensure_sidebar.sh"
else
  fail "@tabby_spawning guard (line ${GUARD_LINE:-?}) should be before USR1 signal (line ${USR1_LINE:-?}) in ensure_sidebar.sh"
fi

# ─── Test 7: Script uses -P -F to capture new window ID ───

echo ""
echo "--- Test 7: Captures new window ID via -P -F ---"

# The script no longer uses -d; focus is handled explicitly via select-window/switch-client.
# It does use -P -F to capture the new window ID for group assignment and focus.
if grep -q 'new-window -P -F' "$PROJECT_ROOT/scripts/new_window_with_group.sh"; then
  pass "Script captures new window ID with -P -F"
else
  fail "Script missing -P -F for window ID capture"
fi

# ─── Test 8: No duplicate exit statements ───

echo ""
echo "--- Test 8: No duplicate exit statements ---"

EXIT_COUNT=$(grep -c '^exit 0' "$PROJECT_ROOT/scripts/new_window_with_group.sh" || echo "0")
if [ "$EXIT_COUNT" -le 1 ]; then
  pass "Single exit statement (count=$EXIT_COUNT)"
else
  fail "Found $EXIT_COUNT 'exit 0' statements (expected 1)"
fi

# ─── Test 9: No duplicate focus blocks ───

echo ""
echo "--- Test 9: Single focus block (no duplication) ---"

FOCUS_BLOCKS=$(grep -c 'switch-client.*CLIENT_TTY.*NEW_WINDOW_ID' "$PROJECT_ROOT/scripts/new_window_with_group.sh" || echo "0")
if [ "$FOCUS_BLOCKS" -eq 1 ]; then
  pass "Single focus block (count=$FOCUS_BLOCKS)"
else
  fail "Found $FOCUS_BLOCKS focus blocks (expected 1)"
fi

# ─── Test 10: Working directory respected ───

echo ""
echo "--- Test 10: Custom working directory ---"

tmux set-option -g @tabby_new_window_path "/tmp" 2>/dev/null || true
bash "$PROJECT_ROOT/scripts/new_window_with_group.sh" 2>/dev/null
sleep 0.3
NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
PANE_PATH=$(tmux list-panes -t "$NEWEST_WIN" -F '#{pane_current_path}' 2>/dev/null | head -1)
# /tmp may resolve to /private/tmp on macOS
if [ "$PANE_PATH" = "/tmp" ] || [ "$PANE_PATH" = "/private/tmp" ]; then
  pass "New window opened in /tmp"
else
  fail "New window path is '$PANE_PATH', expected '/tmp'"
fi

SAVED_PATH_AFTER=$(tmux show-option -gqv @tabby_new_window_path 2>/dev/null || echo "")
if [ -z "$SAVED_PATH_AFTER" ]; then
  pass "@tabby_new_window_path cleared after use"
else
  fail "@tabby_new_window_path still set to '$SAVED_PATH_AFTER'"
fi

# ─── Summary ───

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
