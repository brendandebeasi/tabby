#!/usr/bin/env bash
set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-atomic-new-window}"
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-atomic-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
TEST_SESSION="tabby-atomic-new-window-test"

PASS=0
FAIL=0
SKIP=0

cleanup() {
  tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true
  rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}
trap cleanup EXIT

pass() { echo "✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "✗ $1" >&2; FAIL=$((FAIL + 1)); }
skip() { echo "- $1 (SKIP)"; SKIP=$((SKIP + 1)); }

echo "=== Integration Test: Atomic New Window Binary ==="

tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION" -n "main"

SESSION_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{session_id}')"

count_windows() {
  tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | wc -l | tr -d ' '
}

get_window_id_by_name() {
  tmux list-windows -t "$TEST_SESSION" -F '#{window_id}|#{window_name}' | awk -F'|' -v name="$1" '$2 == name { print $1; exit }'
}

count_panes_in_window() {
  local win_id="$1"
  tmux list-panes -t "$win_id" -F '#{pane_id}' | wc -l | tr -d ' '
}

get_pane_command() {
  local pane_id="$1"
  tmux display-message -t "$pane_id" -p '#{pane_current_command}'
}

get_pane_path() {
  local pane_id="$1"
  tmux display-message -t "$pane_id" -p '#{pane_current_path}'
}

# ─── Test 1: Binary exists and is executable ───

echo ""
echo "--- Test 1: Binary exists and is executable ---"

if [ -x "$PROJECT_ROOT/bin/tabby" ]; then
  pass "bin/tabby new-window exists and is executable"
else
  fail "bin/tabby new-window not found or not executable"
fi

# ─── Test 2: Atomic creation — new window gets sidebar pane immediately ───

echo ""
echo "--- Test 2: Atomic creation with sidebar pane ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  tmux set-option -g @tabby_sidebar enabled 2>/dev/null || true
  BEFORE=$(count_windows)
  
  # Run the binary with explicit session ID
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true
  sleep 0.3
  
  AFTER=$(count_windows)
  
  if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
    PANE_COUNT=$(count_panes_in_window "$NEWEST_WIN")
    
    # Should have 2 panes: content + sidebar-renderer
    if [ "$PANE_COUNT" -eq 2 ]; then
      FOUND_SIDEBAR=$(tmux list-panes -t "$NEWEST_WIN" -F '#{pane_start_command}' | grep -c "sidebar-renderer" || true)
      
      if [ "${FOUND_SIDEBAR:-0}" -gt 0 ]; then
        pass "New window has 2 panes with sidebar-renderer"
      else
        fail "New window has 2 panes but sidebar-renderer not found in pane_start_command"
      fi
    else
      fail "New window has $PANE_COUNT panes, expected 2"
    fi
  else
    fail "Expected $((BEFORE + 1)) windows, got $AFTER"
  fi
fi

# ─── Test 3: Focus on content pane — NOT sidebar-renderer ───

echo ""
echo "--- Test 3: Focus on content pane (not sidebar-renderer) ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  BEFORE=$(count_windows)
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true
  sleep 0.3
  AFTER=$(count_windows)
  
  if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
    ACTIVE_PANE=$(tmux display-message -t "$NEWEST_WIN" -p '#{active_pane}')
    ACTIVE_CMD=$(get_pane_command "$ACTIVE_PANE")
    
    if [[ "$ACTIVE_CMD" != *"sidebar-renderer"* ]]; then
      pass "Active pane is not sidebar-renderer (cmd: $ACTIVE_CMD)"
    else
      fail "Active pane is sidebar-renderer, should be content pane"
    fi
   else
     fail "Window not created for focus test"
   fi
 fi

# ─── Test 4: @tabby_spawning guard cleared after binary exits ───

echo ""
echo "--- Test 4: @tabby_spawning cleared after binary exits ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true
  sleep 0.3
  
  SPAWNING=$(tmux show-option -gqv @tabby_spawning 2>/dev/null || echo "")
  if [ -z "$SPAWNING" ] || [ "$SPAWNING" != "1" ]; then
    pass "@tabby_spawning is cleared after binary"
  else
    fail "@tabby_spawning is still set to 1"
  fi
fi

# ─── Test 5: Group assignment via -group flag ───

echo ""
echo "--- Test 5: Group assignment via -group flag ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  BEFORE=$(count_windows)
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" -group "AtomicTestGroup" 2>/dev/null || true
  sleep 0.3
  AFTER=$(count_windows)

  if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
    GROUP=$(tmux show-window-options -t "$NEWEST_WIN" -v @tabby_group 2>/dev/null || echo "")
    if [ "$GROUP" = "AtomicTestGroup" ]; then
      pass "New window has group 'AtomicTestGroup' via -group flag"
    else
      fail "New window group is '$GROUP', expected 'AtomicTestGroup'"
    fi
  else
    fail "Window not created for group test"
  fi
fi

# ─── Test 7: Disabled mode — no sidebar pane ───

echo ""
echo "--- Test 7: Disabled mode (no sidebar pane) ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  tmux set-option -g @tabby_sidebar disabled 2>/dev/null || true
  BEFORE=$(count_windows)
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true
  sleep 0.3
  AFTER=$(count_windows)
  
  if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
    PANE_COUNT=$(count_panes_in_window "$NEWEST_WIN")
    
    if [ "$PANE_COUNT" -eq 1 ]; then
      pass "Disabled mode: new window has exactly 1 pane"
    else
      fail "Disabled mode: new window has $PANE_COUNT panes, expected 1"
    fi
  else
    fail "Window not created for disabled mode test"
  fi
  
  # Re-enable for remaining tests
  tmux set-option -g @tabby_sidebar enabled 2>/dev/null || true
fi

# ─── Test 8: No spurious window switching ───

echo ""
echo "--- Test 8: No spurious window switching ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  tmux set-option -g @tabby_sidebar enabled 2>/dev/null || true
  
  # Record current window before running binary
  BEFORE_WIN=$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')
  
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true
  sleep 0.3
  
  AFTER_WIN=$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')
  
  # After should be the NEW window (not BEFORE)
  if [ "$AFTER_WIN" != "$BEFORE_WIN" ]; then
    # Verify it's actually a new window (not some third window)
    WINDOW_COUNT=$(count_windows)
    if [ "$WINDOW_COUNT" -ge 2 ]; then
      pass "Window switched to new window (from $BEFORE_WIN to $AFTER_WIN)"
    else
      fail "Window switched but only 1 window exists"
    fi
  else
    fail "Window did not switch after binary execution"
  fi
fi

# ─── Test 9: Working directory respected via -path flag ───

echo ""
echo "--- Test 9: Working directory respected via -path flag ---"

if [ ! -x "$PROJECT_ROOT/bin/tabby" ]; then
  skip "bin/tabby new-window not built yet"
else
  "$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" -path "/tmp" 2>/dev/null || true
  sleep 0.3

  NEWEST_WIN=$(tmux list-windows -t "$TEST_SESSION" -F '#{window_id}' | tail -1)
  PANE_PATH=""
  while IFS= read -r line; do
    pane_id="${line%%|*}"
    start_cmd="${line#*|}"
    if [[ "$start_cmd" != *"sidebar-renderer"* ]]; then
      PANE_PATH=$(tmux display-message -t "$pane_id" -p '#{pane_current_path}')
      break
    fi
  done < <(tmux list-panes -t "$NEWEST_WIN" -F '#{pane_id}|#{pane_start_command}' 2>/dev/null)

  # /tmp may resolve to /private/tmp on macOS
  if [ "$PANE_PATH" = "/tmp" ] || [ "$PANE_PATH" = "/private/tmp" ]; then
    pass "New window opened in /tmp via -path flag (path: $PANE_PATH)"
  else
    fail "New window path is '$PANE_PATH', expected '/tmp'"
  fi
fi

# ─── Summary ───

echo ""
echo "=== Results: $PASS passed, $FAIL failed, $SKIP skipped ==="
[ "$FAIL" -eq 0 ] || exit 1
