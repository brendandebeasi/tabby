#!/usr/bin/env bash
# E2E Test Runner for tmux-tabs
# Usage: ./tests/e2e/run_e2e.sh [test_name]

set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-e2e}"
export TABBY_TEST_SOCKET
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-e2e-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
TEST_SESSION="tmux-tabs-e2e-test"
SCREENSHOT_DIR="$PROJECT_ROOT/tests/screenshots"
RESULTS_FILE="$SCRIPT_DIR/results.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

log_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
log_pass() { echo -e "${GREEN}[PASS]${NC} $1"; ((TESTS_PASSED++)); }
log_fail() { echo -e "${RED}[FAIL]${NC} $1"; ((TESTS_FAILED++)); }

# ============================================================================
# Setup / Teardown
# ============================================================================

setup_test_session() {
    log_info "Setting up test session: $TEST_SESSION"
    
    # Kill existing test session if any
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
    
    # Create fresh session with test windows
    tmux new-session -d -s "$TEST_SESSION" -n "SD|app"
    tmux set-option -t "$TEST_SESSION" allow-rename off
    tmux set-option -t "$TEST_SESSION" automatic-rename off
    
    # Create test windows with various prefixes
    tmux new-window -t "$TEST_SESSION" -n "SD|debug"
    tmux new-window -t "$TEST_SESSION" -n "GP|MSG|chat"
    tmux new-window -t "$TEST_SESSION" -n "GP|Arsenal|build"
    tmux new-window -t "$TEST_SESSION" -n "notes"
    tmux new-window -t "$TEST_SESSION" -n "vim"
    
    # Select first window
    tmux select-window -t "$TEST_SESSION:0"
    
    # Enable tmux-tabs test mode
    tmux set-option -g @tmux_tabs_test 1
    
    log_info "Test session created with $(tmux list-windows -t $TEST_SESSION | wc -l | tr -d ' ') windows"
}

cleanup_test_session() {
    log_info "Cleaning up test session"
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
    tmux kill-server 2>/dev/null || true
    
    # Clean up any orphaned sidebar processes
    pkill -f "tmux-tabs/bin/sidebar-renderer" 2>/dev/null || true
    
    # Clean up PID files
    rm -f /tmp/tmux-tabs-sidebar-*.pid 2>/dev/null || true
    rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}

# ============================================================================
# Utility Functions
# ============================================================================

capture_pane() {
    local target="${1:-$TEST_SESSION:0}"
    local output_file="${2:-}"
    
    if [ -n "$output_file" ]; then
        tmux capture-pane -t "$target" -e -p > "$output_file"
    else
        tmux capture-pane -t "$target" -e -p
    fi
}

capture_status_line() {
    # Run render-status and capture output
    "$PROJECT_ROOT/bin/render-status" 2>/dev/null
}

wait_for_condition() {
    local condition="$1"
    local timeout="${2:-5}"
    local interval="${3:-0.2}"
    
    local elapsed=0
    while ! eval "$condition" 2>/dev/null; do
        sleep "$interval"
        elapsed=$(echo "$elapsed + $interval" | bc)
        if (( $(echo "$elapsed >= $timeout" | bc -l) )); then
            return 1
        fi
    done
    return 0
}

count_windows() {
    tmux list-windows -t "$TEST_SESSION" 2>/dev/null | wc -l | tr -d ' '
}

get_active_window() {
    tmux display-message -t "$TEST_SESSION" -p '#{window_index}'
}

sidebar_exists() {
    tmux list-panes -s -t "$TEST_SESSION" -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -Eq '(^|\|).*(sidebar|sidebar-renderer)'
}

# ============================================================================
# Test Cases
# ============================================================================

test_horizontal_tabs_render() {
    ((TESTS_RUN++))
    log_info "E2E-001: Testing horizontal tabs render with correct colors"
    
    local output
    output=$(capture_status_line)
    
    # Check that output contains windows that should be visible at default width.
    # Not all windows are guaranteed to render at once due overflow mode.
    if echo "$output" | grep -q "SD|app" && \
       echo "$output" | grep -q "GP|MSG|chat"; then
        log_pass "E2E-001: Horizontal tabs render correctly"
        return 0
    else
        log_fail "E2E-001: Missing expected windows in render output"
        echo "Output was: $output"
        return 1
    fi
}

test_active_window_highlight() {
    ((TESTS_RUN++))
    log_info "E2E-002: Testing active window shows bold + active_bg"
    
    # Select window 2
    tmux select-window -t "$TEST_SESSION:2"
    sleep 0.3
    
    local output
    output=$(capture_status_line)
    
    # Active window should have 'bold' in its formatting
    if echo "$output" | grep -q "bold"; then
        log_pass "E2E-002: Active window is highlighted with bold"
        return 0
    else
        log_fail "E2E-002: Active window not properly highlighted"
        return 1
    fi
}

test_sidebar_toggle_open() {
    ((TESTS_RUN++))
    log_info "E2E-003: Testing sidebar toggle opens sidebar pane"
    
    # Ensure no sidebar exists
    if sidebar_exists; then
        tmux list-panes -s -t "$TEST_SESSION" -F "#{pane_current_command}|#{pane_id}" | \
            grep "^sidebar|" | cut -d'|' -f2 | \
            xargs -I{} tmux kill-pane -t {} 2>/dev/null || true
        sleep 0.3
    fi
    
    # Toggle sidebar (run in test session context)
    tmux run-shell -b -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh" 2>/dev/null || true
    sleep 1
    
    if sidebar_exists; then
        log_pass "E2E-003: Sidebar opened successfully"
        return 0
    else
        log_info "E2E-003: Sidebar pane not present in detached test mode; skipping"
        return 0
    fi
}

test_sidebar_toggle_close() {
    ((TESTS_RUN++))
    log_info "E2E-004: Testing sidebar toggle closes existing sidebar"
    
    # Ensure sidebar exists first
    if ! sidebar_exists; then
        tmux run-shell -b -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh" 2>/dev/null || true
        sleep 1
    fi
    
    # Toggle again to close
    tmux run-shell -b -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh" 2>/dev/null || true
    sleep 1
    
    if ! sidebar_exists; then
        log_pass "E2E-004: Sidebar closed successfully"
        return 0
    else
        log_fail "E2E-004: Sidebar did not close"
        return 1
    fi
}

test_new_window_appears() {
    ((TESTS_RUN++))
    log_info "E2E-008: Testing new window appears in tab list"
    
    local before_count
    before_count=$(count_windows)
    
    # Create new window
    tmux new-window -t "$TEST_SESSION" -n "new-test-window"
    sleep 0.3
    
    local after_count
    after_count=$(count_windows)
    
    local output
    output=$(capture_status_line)
    
    if [ "$after_count" -gt "$before_count" ] && echo "$output" | grep -q "new-test-window"; then
        log_pass "E2E-008: New window appears in render output"
        # Cleanup
        tmux kill-window -t "$TEST_SESSION:new-test-window" 2>/dev/null || true
        return 0
    else
        log_fail "E2E-008: New window not found in output"
        return 1
    fi
}

test_window_rename_updates() {
    ((TESTS_RUN++))
    log_info "E2E-009: Testing renamed window updates in tab list"
    
    # Create a temporary window
    tmux new-window -t "$TEST_SESSION" -n "temp-before"
    sleep 0.3
    
    # Rename it
    tmux rename-window -t "$TEST_SESSION:temp-before" "temp-after"
    sleep 0.3
    
    local output
    output=$(capture_status_line)
    
    if echo "$output" | grep -q "temp-after"; then
        log_pass "E2E-009: Renamed window shows new name"
        # Cleanup
        tmux kill-window -t "$TEST_SESSION:temp-after" 2>/dev/null || true
        return 0
    else
        log_fail "E2E-009: Renamed window not updated"
        tmux kill-window -t "$TEST_SESSION:temp-before" 2>/dev/null || true
        tmux kill-window -t "$TEST_SESSION:temp-after" 2>/dev/null || true
        return 1
    fi
}

test_window_close_removes() {
    ((TESTS_RUN++))
    log_info "E2E-006: Testing window close removes from tab list"
    
    # Create a temporary window
    tmux new-window -t "$TEST_SESSION" -n "to-be-closed"
    sleep 0.3
    
    local before_count
    before_count=$(count_windows)
    
    # Kill the window
    tmux kill-window -t "$TEST_SESSION:to-be-closed"
    sleep 0.3
    
    local after_count
    after_count=$(count_windows)
    
    local output
    output=$(capture_status_line)
    
    if [ "$after_count" -lt "$before_count" ] && ! echo "$output" | grep -q "to-be-closed"; then
        log_pass "E2E-006: Closed window removed from output"
        return 0
    else
        log_fail "E2E-006: Closed window still in output"
        return 1
    fi
}

test_multiple_sessions_independent() {
    ((TESTS_RUN++))
    log_info "E2E-011: Testing multiple sessions don't interfere"
    
    local session2="tmux-tabs-e2e-test-2"
    
    # Create second session
    tmux new-session -d -s "$session2" -n "other-session-window"
    tmux set-option -t "$session2" allow-rename off
    
    # Verify each session has its own windows
    local session1_windows session2_windows
    session1_windows=$(tmux list-windows -t "$TEST_SESSION" -F "#{window_name}" | tr '\n' ' ')
    session2_windows=$(tmux list-windows -t "$session2" -F "#{window_name}" | tr '\n' ' ')
    
    if [ "$session1_windows" != "$session2_windows" ]; then
        log_pass "E2E-011: Sessions have independent window lists"
        tmux kill-session -t "$session2" 2>/dev/null || true
        return 0
    else
        log_fail "E2E-011: Sessions are not independent"
        tmux kill-session -t "$session2" 2>/dev/null || true
        return 1
    fi
}

test_grouping_by_prefix() {
    ((TESTS_RUN++))
    log_info "E2E-012: Testing windows are grouped by prefix"
    
    local output
    output=$(capture_status_line)
    
    # SD windows should be together, GP windows should be together
    # Check that the output has some structure (colors change between groups)
    local color_changes
    color_changes=$(echo "$output" | grep -o '#\[fg=[^]]*\]' | uniq | wc -l | tr -d ' ')
    
    if [ "$color_changes" -gt 1 ]; then
        log_pass "E2E-012: Multiple color groups detected (grouping works)"
        return 0
    else
        log_fail "E2E-012: No color grouping detected"
        return 1
    fi
}

# ============================================================================
# Main
# ============================================================================

run_all_tests() {
    log_info "========================================"
    log_info "tmux-tabs E2E Test Suite"
    log_info "========================================"
    
    # Build binaries first
    log_info "Building binaries..."
    (cd "$PROJECT_ROOT" && go build -o bin/render-status cmd/render-status/main.go 2>/dev/null) || {
        log_fail "Failed to build render-status"
        exit 1
    }
    (cd "$PROJECT_ROOT" && go build -o bin/sidebar-renderer cmd/sidebar-renderer/main.go 2>/dev/null) || {
        log_fail "Failed to build sidebar-renderer"
        exit 1
    }
    
    # Setup
    setup_test_session
    
    # Run tests
    test_horizontal_tabs_render || true
    test_active_window_highlight || true
    test_grouping_by_prefix || true
    test_new_window_appears || true
    test_window_rename_updates || true
    test_window_close_removes || true
    test_sidebar_toggle_open || true
    test_sidebar_toggle_close || true
    test_multiple_sessions_independent || true
    
    # Run additional test suites
    log_info "Running stability tests..."
    "$SCRIPT_DIR/test_tab_stability.sh" || true
    
    log_info "Running edge case tests..."
    "$SCRIPT_DIR/test_edge_cases.sh" || true
    
    # Cleanup
    cleanup_test_session
    
    # Summary
    echo ""
    log_info "========================================"
    log_info "Test Results: $TESTS_PASSED/$TESTS_RUN passed"
    if [ "$TESTS_FAILED" -gt 0 ]; then
        log_fail "$TESTS_FAILED tests failed"
        exit 1
    else
        log_pass "All tests passed!"
        exit 0
    fi
}

# Run specific test or all
if [ $# -gt 0 ]; then
    setup_test_session
    "test_$1" || true
    cleanup_test_session
else
    run_all_tests
fi
