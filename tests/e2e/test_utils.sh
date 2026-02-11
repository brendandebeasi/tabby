#!/usr/bin/env bash

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests}"

if [ -z "${TABBY_TMUX_WRAPPED:-}" ]; then
    TABBY_TMUX_REAL="$(command -v tmux)"
    TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-tmux.XXXXXX)"
    cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
    chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
    export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"
    export TABBY_TMUX_WRAPPED=1
fi

tmux() { command tmux "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"

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
    tmux kill-server 2>/dev/null || true
    pkill -f "tabby/bin/sidebar-renderer" 2>/dev/null || true
    rm -f /tmp/tabby-sidebar-*.state 2>/dev/null || true
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
    tmux list-panes -s -t "$session_name" -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -Eq '(^|\|).*(sidebar|sidebar-renderer)'
}
