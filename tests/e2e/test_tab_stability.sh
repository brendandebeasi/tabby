#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_utils.sh"

test_rapid_window_switching() {
    local initial_count=$(tmux list-windows | wc -l | tr -d ' ')
    
    for i in {0..20}; do
        local target=$((i % initial_count))
        tmux select-window -t "$target"
        sleep 0.05
    done
    
    sleep 0.5
    local final_count=$(tmux list-windows | wc -l | tr -d ' ')
    
    if [ "$initial_count" -eq "$final_count" ]; then
        log_pass "Rapid switching: All windows survived ($final_count windows)"
    else
        log_fail "Rapid switching: Lost windows! Initial: $initial_count, Final: $final_count"
        tmux list-windows
    fi
}

test_window_index_consistency() {
    local test_name="window_index_consistency"
    setup_test_session "$test_name"
    
    tmux new-window -t "$test_name:1" -n "window-one"
    tmux new-window -t "$test_name:2" -n "window-two"
    tmux new-window -t "$test_name:3" -n "window-three"
    
    local initial_state=$(tmux list-windows -t "$test_name" -F "#{window_index}:#{window_name}")
    
    tmux select-window -t "$test_name:1"
    tmux rename-window -t "$test_name:2" "window-two-renamed"
    tmux select-window -t "$test_name:3"
    
    local status_output=$(tmux switch-client -t "$test_name" 2>/dev/null; "$PROJECT_ROOT/bin/render-status")
    
    if echo "$status_output" | grep -q "1:window-one" && \
       echo "$status_output" | grep -q "2:window-two-renamed" && \
       echo "$status_output" | grep -q "3:window-three"; then
        log_pass "Window indices: Consistent after operations"
    else
        log_fail "Window indices: Mismatch detected!"
        echo "Expected to find: 1:window-one, 2:window-two-renamed, 3:window-three"
        echo "Actual output: $status_output"
    fi
    
    cleanup_test_session "$test_name"
}

test_sidebar_status_sync() {
    local test_name="sidebar_status_sync"
    setup_test_session "$test_name"
    
    create_test_windows 4 "$test_name"
    
    tmux run-shell -t "$test_name" "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
    sleep 1
    
    local tmux_windows=$(tmux list-windows -t "$test_name" -F "#{window_index}:#{window_name}" | sort)
    local status_output=$(tmux switch-client -t "$test_name" 2>/dev/null; "$PROJECT_ROOT/bin/render-status")
    local sidebar_pane=$(tmux list-panes -s -t "$test_name" -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar|" | cut -d'|' -f2)
    local sidebar_content=$(tmux capture-pane -t "$sidebar_pane" -p | grep -E "^\s*\[[0-9]+\]" | sed 's/^[[:space:]]*//')
    
    local all_match=true
    echo "=== Window Sync Check ==="
    echo "Tmux windows: $tmux_windows"
    echo "Status bar contains all windows: "
    while IFS= read -r window; do
        if echo "$status_output" | grep -q "$window"; then
            echo "  ✓ $window"
        else
            echo "  ✗ $window MISSING"
            all_match=false
        fi
    done <<< "$tmux_windows"
    
    if [ "$all_match" = true ]; then
        log_pass "Window sync: All sources show same windows"
    else
        log_fail "Window sync: Discrepancies found between sources"
    fi
    
    cleanup_test_session "$test_name"
}

test_kill_window_updates() {
    local test_name="kill_window_updates"
    setup_test_session "$test_name"
    
    tmux new-window -t "$test_name:1" -n "to-be-killed"
    tmux new-window -t "$test_name:2" -n "survivor"
    
    tmux run-shell -t "$test_name" "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
    sleep 1
    
    tmux kill-window -t "$test_name:to-be-killed"
    sleep 0.5
    
    local status_output=$(tmux switch-client -t "$test_name" 2>/dev/null; "$PROJECT_ROOT/bin/render-status")
    if echo "$status_output" | grep -q "to-be-killed"; then
        log_fail "Kill window: Dead window still in status bar"
    else
        log_pass "Kill window: Status bar updated correctly"
    fi
    
    local sidebar_pane=$(tmux list-panes -s -t "$test_name" -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar|" | cut -d'|' -f2)
    if [ -n "$sidebar_pane" ]; then
        local sidebar_content=$(tmux capture-pane -t "$sidebar_pane" -p)
        if echo "$sidebar_content" | grep -q "to-be-killed"; then
            log_fail "Kill window: Dead window still in sidebar"
        else
            log_pass "Kill window: Sidebar updated correctly"
        fi
    fi
    
    cleanup_test_session "$test_name"
}

test_stress_operations() {
    local test_name="stress_test"
    setup_test_session "$test_name"
    
    log_info "Running stress test with many operations..."
    
    for i in {1..10}; do
        tmux new-window -t "$test_name" -n "stress-$i"
    done
    
    for i in {1..20}; do
        local op=$((i % 4))
        case $op in
            0)
                local target=$((RANDOM % 10))
                tmux select-window -t "$test_name:$target" 2>/dev/null || true
                ;;
            1)
                local target=$((RANDOM % 10))
                tmux rename-window -t "$test_name:$target" "renamed-$i" 2>/dev/null || true
                ;;
            2)
                tmux new-window -t "$test_name" -n "new-$i"
                ;;
            3)
                local count=$(count_windows "$test_name")
                if [ "$count" -gt 3 ]; then
                    local target=$((RANDOM % count))
                    tmux kill-window -t "$test_name:$target" 2>/dev/null || true
                fi
                ;;
        esac
        sleep 0.1
    done
    
    sleep 0.5
    
    local tmux_count=$(count_windows "$test_name")
    local status_output=$(tmux switch-client -t "$test_name" 2>/dev/null; "$PROJECT_ROOT/bin/render-status")
    local status_window_count=$(echo "$status_output" | grep -o "[0-9]\+:" | wc -l | tr -d ' ')
    
    echo "Final state: $tmux_count windows in tmux"
    echo "Status bar shows: $status_window_count windows"
    
    if [ "$tmux_count" -gt 0 ] && [ "$tmux_count" -eq "$status_window_count" ]; then
        log_pass "Stress test: System remained consistent ($tmux_count windows)"
    else
        log_fail "Stress test: Inconsistency detected! Tmux: $tmux_count, Status: $status_window_count"
    fi
    
    cleanup_test_session "$test_name"
}

main() {
    echo "========================================"
    echo "Tab Stability Test Suite"
    echo "========================================"
    
    test_rapid_window_switching
    test_window_index_consistency
    test_sidebar_status_sync
    test_kill_window_updates
    test_stress_operations
    
    echo "========================================"
    echo "Test Summary: $TESTS_PASSED/$TESTS_RUN passed"
    echo "========================================"
    
    if [ "$TESTS_FAILED" -gt 0 ]; then
        exit 1
    fi
}

main "$@"
