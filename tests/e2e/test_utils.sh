#!/usr/bin/env bash

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

log_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
log_pass() { echo -e "${GREEN}[PASS]${NC} $1"; ((TESTS_PASSED++)); ((TESTS_RUN++)); }
log_fail() { echo -e "${RED}[FAIL]${NC} $1"; ((TESTS_FAILED++)); ((TESTS_RUN++)); }

setup_test_session() {
    local session_name="${1:-test-session}"
    tmux kill-session -t "$session_name" 2>/dev/null || true
    tmux new-session -d -s "$session_name" -n "default"
    tmux set-option -t "$session_name" allow-rename off
    tmux set-option -t "$session_name" automatic-rename off
    tmux set-option -g @tmux_tabs_test 1
}

cleanup_test_session() {
    local session_name="${1:-test-session}"
    tmux kill-session -t "$session_name" 2>/dev/null || true
    pkill -f "tmux-tabs/bin/sidebar" 2>/dev/null || true
    rm -f /tmp/tmux-tabs-sidebar-*.state 2>/dev/null || true
}

create_test_windows() {
    local count="${1:-3}"
    local session_name="${2:-test-session}"
    
    for i in $(seq 1 "$count"); do
        tmux new-window -t "$session_name" -n "test-window-$i"
    done
}

count_windows() {
    local session_name="${1:-test-session}"
    tmux list-windows -t "$session_name" 2>/dev/null | wc -l | tr -d ' '
}

get_active_window() {
    local session_name="${1:-test-session}"
    tmux display-message -t "$session_name" -p '#{window_index}'
}

sidebar_exists() {
    local session_name="${1:-test-session}"
    tmux list-panes -s -t "$session_name" -F "#{pane_current_command}" 2>/dev/null | grep -q "^sidebar$"
}
