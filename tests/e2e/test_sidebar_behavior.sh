#!/usr/bin/env bash
# Automated behavioral tests for Tabby sidebar
# Run from within a tmux session with Tabby enabled

set -e

PASS=0
FAIL=0

log_pass() { echo "[PASS] $1"; ((PASS++)); }
log_fail() { echo "[FAIL] $1"; ((FAIL++)); }
log_info() { echo "[INFO] $1"; }

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# Check we're in tmux
if [ -z "$TMUX" ]; then
    echo "ERROR: Must run inside tmux session"
    exit 1
fi

echo "========================================"
echo "Tabby Sidebar Behavioral Tests"
echo "========================================"
echo ""

# Setup: Record initial state
INITIAL_WINDOWS=$(tmux list-windows | wc -l | tr -d ' ')
log_info "Initial windows: $INITIAL_WINDOWS"

# Test 1: Window Creation
echo ""
echo "Test 1: Window Creation"
BEFORE=$(tmux list-windows | wc -l | tr -d ' ')
tmux new-window -d -n "test-create"
sleep 0.3
AFTER=$(tmux list-windows | wc -l | tr -d ' ')
if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    log_pass "Window created successfully"
else
    log_fail "Window creation failed (before=$BEFORE, after=$AFTER)"
fi

# Test 2: Window Rename
echo ""
echo "Test 2: Window Rename"
tmux rename-window -t :test-create "test-renamed"
sleep 0.2
NAME=$(tmux display-message -t :test-renamed -p '#{window_name}' 2>/dev/null || echo "NOT_FOUND")
if [ "$NAME" = "test-renamed" ]; then
    log_pass "Window renamed successfully"
else
    log_fail "Window rename failed (expected: test-renamed, got: $NAME)"
fi

# Test 3: Auto-rename Lock
echo ""
echo "Test 3: Auto-rename Lock After Manual Rename"
AUTO_RENAME=$(tmux show-window-options -t :test-renamed -v automatic-rename 2>/dev/null || echo "on")
if [ "$AUTO_RENAME" = "off" ]; then
    log_pass "Auto-rename locked after manual rename"
else
    log_fail "Auto-rename not locked (value: $AUTO_RENAME)"
fi

# Test 4: Pane Split - Horizontal
echo ""
echo "Test 4: Pane Split (Horizontal)"
tmux select-window -t :test-renamed
BEFORE=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
tmux split-window -h -t :test-renamed
sleep 0.2
AFTER=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
if [ "$AFTER" -gt "$BEFORE" ]; then
    log_pass "Horizontal split created pane (before=$BEFORE, after=$AFTER)"
else
    log_fail "Horizontal split failed (before=$BEFORE, after=$AFTER)"
fi

# Test 5: Pane Split - Vertical
echo ""
echo "Test 5: Pane Split (Vertical)"
BEFORE=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
tmux split-window -v -t :test-renamed
sleep 0.2
AFTER=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
if [ "$AFTER" -gt "$BEFORE" ]; then
    log_pass "Vertical split created pane (before=$BEFORE, after=$AFTER)"
else
    log_fail "Vertical split failed (before=$BEFORE, after=$AFTER)"
fi

# Test 6: Pane Close
echo ""
echo "Test 6: Pane Close"
PANES_BEFORE=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
# Get a non-sidebar pane to kill
PANE_TO_KILL=$(tmux list-panes -t :test-renamed -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar' | head -1 | cut -d: -f1)
if [ -n "$PANE_TO_KILL" ]; then
    tmux kill-pane -t "$PANE_TO_KILL"
    sleep 0.2
    PANES_AFTER=$(tmux list-panes -t :test-renamed -F '#{pane_id}' | grep -cv '^$' || echo 0)
    if [ "$PANES_AFTER" -lt "$PANES_BEFORE" ]; then
        log_pass "Pane closed successfully"
    else
        log_fail "Pane close failed"
    fi
else
    log_fail "No pane found to close"
fi

# Test 7: Group Assignment via @tabby_group option
echo ""
echo "Test 7: Group Assignment via @tabby_group"
tmux new-window -d -n "test-group"
sleep 0.2
# Get the window index
WIN_INDEX=$(tmux list-windows -F '#{window_name}:#{window_index}' | grep "^test-group:" | cut -d: -f2)
# Set the group option
tmux set-window-option -t ":${WIN_INDEX}" @tabby_group "StudioDome"
sleep 0.2
# Verify the option was set
GROUP_VALUE=$(tmux show-window-options -t ":${WIN_INDEX}" -v @tabby_group 2>/dev/null || echo "")
if [ "$GROUP_VALUE" = "StudioDome" ]; then
    log_pass "Group option set (@tabby_group=StudioDome)"
else
    log_fail "Group option failed (got: $GROUP_VALUE)"
fi

# Test 8: Remove from Group
echo ""
echo "Test 8: Remove from Group"
# Unset the group option
tmux set-window-option -t ":${WIN_INDEX}" -u @tabby_group
sleep 0.2
# Verify the option was unset
GROUP_VALUE=$(tmux show-window-options -t ":${WIN_INDEX}" -v @tabby_group 2>/dev/null || echo "")
if [ -z "$GROUP_VALUE" ]; then
    log_pass "Group option removed"
else
    log_fail "Failed to remove group option (got: $GROUP_VALUE)"
fi
# Rename window for later cleanup
tmux rename-window -t ":${WIN_INDEX}" "test-group-plain"

# Test 9: Window Kill
echo ""
echo "Test 9: Window Kill"
BEFORE=$(tmux list-windows | wc -l | tr -d ' ')
tmux kill-window -t :test-group-plain 2>/dev/null || true
sleep 0.3
AFTER=$(tmux list-windows | wc -l | tr -d ' ')
if [ "$AFTER" -lt "$BEFORE" ]; then
    log_pass "Window killed successfully"
else
    log_fail "Window kill failed"
fi

# Test 10: Signal Sidebar Refresh
echo ""
echo "Test 10: Signal Sidebar Refresh"
SESSION_ID=$(tmux display-message -p '#{session_id}')
PID_FILE="/tmp/tabby-sidebar-${SESSION_ID}.pid"
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        kill -USR1 "$PID" 2>/dev/null
        log_pass "Sidebar signal sent (PID: $PID)"
    else
        log_fail "Sidebar process not running (stale PID file)"
    fi
else
    log_fail "Sidebar PID file not found"
fi

# Test 11: Orphan Window Cleanup
echo ""
echo "Test 11: Orphan Window Cleanup"
tmux new-window -n "test-orphan"
sleep 0.3

# Check window exists
ORPHAN_EXISTS=$(tmux list-windows -F '#{window_name}' | grep -c "test-orphan" || echo 0)
if [ "$ORPHAN_EXISTS" -gt 0 ]; then
    # Count non-sidebar panes
    NON_SIDEBAR=$(tmux list-panes -t :test-orphan -F '#{pane_current_command}' | grep -cv '^sidebar$' || echo 0)

    if [ "$NON_SIDEBAR" -gt 0 ]; then
        # Kill all non-sidebar panes
        for pane in $(tmux list-panes -t :test-orphan -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar' | cut -d: -f1); do
            tmux kill-pane -t "$pane" 2>/dev/null || true
        done
        sleep 0.5

        # Check if window auto-closed
        ORPHAN_EXISTS=$(tmux list-windows -F '#{window_name}' | grep -c "test-orphan" || echo 0)
        if [ "$ORPHAN_EXISTS" -eq 0 ]; then
            log_pass "Orphan window auto-closed"
        else
            log_fail "Orphan window NOT auto-closed"
            tmux kill-window -t :test-orphan 2>/dev/null || true
        fi
    else
        log_fail "No non-sidebar panes found in test window"
        tmux kill-window -t :test-orphan 2>/dev/null || true
    fi
else
    log_fail "Test window not created"
fi

# Cleanup: Kill remaining test windows
echo ""
log_info "Cleaning up test windows..."
tmux kill-window -t :test-renamed 2>/dev/null || true

# Final Summary
echo ""
echo "========================================"
if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
else
    echo -e "${RED}Some tests failed${NC}"
fi
echo "Results: $PASS passed, $FAIL failed"
echo "========================================"

# Verify we didn't leave extra windows
FINAL_WINDOWS=$(tmux list-windows | wc -l | tr -d ' ')
if [ "$FINAL_WINDOWS" -ne "$INITIAL_WINDOWS" ]; then
    log_info "WARNING: Window count changed (was $INITIAL_WINDOWS, now $FINAL_WINDOWS)"
fi

exit $FAIL
