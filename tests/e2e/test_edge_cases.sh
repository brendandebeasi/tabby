#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

source "$SCRIPT_DIR/test_utils.sh"

test_window_renumbering_consistency() {
    log_info "Testing window renumbering consistency..."
    
    local session="edge-test"
    setup_test_session "$session"
    
    tmux new-window -t "$session:1" -n "win-1"
    tmux new-window -t "$session:2" -n "win-2" 
    tmux new-window -t "$session:3" -n "win-3"
    tmux new-window -t "$session:4" -n "win-4"
    sleep 0.5
    
    log_info "Killing window 2 (should cause renumbering)..."
    tmux kill-window -t "$session:2"
    sleep 0.5
    
    local windows_after=$(tmux list-windows -t "$session" -F "#{window_index}:#{window_name}")
    echo "Windows after kill: $windows_after"
    
    if echo "$windows_after" | grep -q "2:win-3" && echo "$windows_after" | grep -q "3:win-4"; then
        log_pass "Windows renumbered correctly"
    else
        log_fail "Window renumbering failed"
    fi
    
    cleanup_test_session "$session"
}

test_sidebar_signal_delivery() {
    log_info "Testing sidebar signal delivery..."
    
    local session="signal-test"
    setup_test_session "$session"
    
    tmux run-shell -t "$session" "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
    sleep 1
    
    local sidebar_pane=$(tmux list-panes -s -t "$session" -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar|" | cut -d'|' -f2)
    
    if [ -n "$sidebar_pane" ]; then
        local sidebar_pid=$(tmux list-panes -a -F "#{pane_id} #{pane_pid}" | grep "^${sidebar_pane} " | cut -d' ' -f2)
        
        log_info "Sending SIGUSR1 to sidebar PID $sidebar_pid..."
        kill -USR1 "$sidebar_pid" 2>/dev/null || log_fail "Failed to send signal"
        
        log_pass "Signal sent successfully"
    else
        log_fail "No sidebar found to test signals"
    fi
    
    cleanup_test_session "$session"
}

test_concurrent_operations() {
    log_info "Testing concurrent window operations..."
    
    local session="concurrent-test"
    setup_test_session "$session"
    
    (
        for i in {1..5}; do
            tmux new-window -t "$session" -n "concurrent-$i"
            sleep 0.1
        done
    ) &
    
    (
        sleep 0.2
        for i in {0..3}; do
            tmux select-window -t "$session:$i" 2>/dev/null || true
            sleep 0.05
        done
    ) &
    
    wait
    sleep 0.5
    
    local final_count=$(count_windows "$session")
    if [ "$final_count" -ge 5 ]; then
        log_pass "Concurrent operations handled ($final_count windows)"
    else
        log_fail "Lost windows during concurrent ops (only $final_count remain)"
    fi
    
    cleanup_test_session "$session"
}

main() {
    echo "========================================"
    echo "Edge Case Test Suite"
    echo "========================================"
    
    test_window_renumbering_consistency
    test_sidebar_signal_delivery
    test_concurrent_operations
    
    echo "========================================"
    echo "Test Summary: $TESTS_PASSED/$TESTS_RUN passed"
    echo "========================================"
    
    if [ "$TESTS_FAILED" -gt 0 ]; then
        exit 1
    fi
}

main "$@"
