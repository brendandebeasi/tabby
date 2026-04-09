#!/usr/bin/env bash
# E2E tests for hot-path shell-to-Go migration (Step 1: pane dimming)
#
# Verifies that the daemon's ApplyPaneDimming() correctly sets per-pane
# window-style and @tabby_pane_dim flags after a USR1 signal, replacing
# the old cycle-pane --dim-only shell invocation.
#
# Requirements: tmux, tabby-daemon binary built at bin/tabby-daemon

set -uo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_utils.sh"

SESSION="dim-test"
DAEMON_BIN="$PROJECT_ROOT/bin/tabby-daemon"

# Build daemon if not present
if [ ! -x "$DAEMON_BIN" ]; then
    log_info "Building tabby-daemon..."
    (cd "$PROJECT_ROOT" && go build -o bin/tabby-daemon ./cmd/tabby-daemon)
fi

echo "========================================"
echo "Hot Path Migration E2E: Pane Dimming"
echo "========================================"
echo ""

# --- Setup ---
setup_test_session "$SESSION"

# Enable dim in config for the test (daemon reads from TABBY_CONFIG_DIR)
TEST_CONFIG_DIR=$(mktemp -d /tmp/tabby-test-config.XXXXXX)
TEST_CONFIG="$TEST_CONFIG_DIR/config.yaml"
cat > "$TEST_CONFIG" <<EOF
pane_header:
  dim_inactive: true
  dim_opacity: 0.8
  terminal_bg: "#1a1a1a"
EOF
export TABBY_CONFIG_DIR="$TEST_CONFIG_DIR"
export TABBY_RUNTIME_PREFIX="test-dim-"

# Create a second pane so we have active + inactive
tmux split-window -t "$SESSION" -h
sleep 0.2

# Record pane IDs
PANE_IDS=$(tmux list-panes -t "$SESSION" -F '#{pane_id}')
PANE1=$(echo "$PANE_IDS" | head -1)
PANE2=$(echo "$PANE_IDS" | tail -1)

# --- Helper functions ---
get_pane_style() {
    local val
    val=$(tmux show-options -p -t "$1" -v window-style 2>/dev/null) || true
    echo "$val"
}

get_dim_flag() {
    local val
    val=$(tmux show-options -p -t "$1" -v @tabby_pane_dim 2>/dev/null) || true
    echo "$val"
}

# Start daemon in background
SESSION_ID=$(tmux display-message -t "$SESSION" -p '#{session_id}')
DAEMON_LOG=$(mktemp /tmp/tabby-test-daemon.XXXXXX.log)
"$DAEMON_BIN" -session "$SESSION_ID" > "$DAEMON_LOG" 2>&1 &
DAEMON_PID=$!
sleep 3  # let daemon start and complete initial refresh cycle

cleanup() {
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
    cleanup_test_session "$SESSION"
    rm -rf "$TEST_CONFIG_DIR"
    rm -f /tmp/test-dim-tabby-daemon-* 2>/dev/null || true
    rm -f "$DAEMON_LOG" 2>/dev/null || true
    true  # don't propagate daemon's SIGTERM exit code
}
trap cleanup EXIT

# --- Test 1: Daemon applies dim after USR1 signal ---
echo ""
echo "Test 1: Daemon applies pane dim styles on signal"

# Focus pane1 (makes pane2 inactive)
tmux select-pane -t "$PANE1"
sleep 0.3

# Signal daemon to trigger refresh (USR1)
# NOTE: The daemon now does significant work per signal (SaveWindowLayouts,
# EnforceStatusExclusivity, etc.) and drains queued signals after each run.
# We must wait long enough for the handler to fully complete before sending
# the next signal, otherwise it gets drained.
kill -USR1 "$DAEMON_PID" 2>/dev/null
sleep 3

STYLE2=$(get_pane_style "$PANE2")
FLAG1=$(get_dim_flag "$PANE1")
FLAG2=$(get_dim_flag "$PANE2")

if echo "$STYLE2" | grep -q "bg=#"; then
    log_pass "Inactive pane has dim background style: $STYLE2"
else
    log_fail "Inactive pane missing dim style (got: '$STYLE2')"
fi

if [ "$FLAG1" = "0" ]; then
    log_pass "Active pane dim flag = 0"
else
    log_fail "Active pane dim flag wrong (expected '0', got '$FLAG1')"
fi

if [ "$FLAG2" = "1" ]; then
    log_pass "Inactive pane dim flag = 1"
else
    log_fail "Inactive pane dim flag wrong (expected '1', got '$FLAG2')"
fi

# --- Test 2: Switching focus updates dim ---
echo ""
echo "Test 2: Switching active pane updates dim styles"

tmux select-pane -t "$PANE2"
sleep 0.3
kill -USR1 "$DAEMON_PID" 2>/dev/null
sleep 3

STYLE1_AFTER=$(get_pane_style "$PANE1")
STYLE2_AFTER=$(get_pane_style "$PANE2")
FLAG1_AFTER=$(get_dim_flag "$PANE1")
FLAG2_AFTER=$(get_dim_flag "$PANE2")

# Now pane1 should be dimmed, pane2 should be clear
if echo "$STYLE1_AFTER" | grep -q "bg=#"; then
    log_pass "Previously active pane now has dim style: $STYLE1_AFTER"
else
    log_fail "Previously active pane missing dim style (got: '$STYLE1_AFTER')"
fi

if [ -z "$STYLE2_AFTER" ] || [ "$STYLE2_AFTER" = "" ]; then
    log_pass "Newly active pane has style cleared"
else
    log_fail "Newly active pane still has style (got: '$STYLE2_AFTER')"
fi

if [ "$FLAG1_AFTER" = "1" ]; then
    log_pass "Previously active pane dim flag = 1"
else
    log_fail "Previously active pane dim flag wrong (expected '1', got '$FLAG1_AFTER')"
fi

if [ "$FLAG2_AFTER" = "0" ]; then
    log_pass "Newly active pane dim flag = 0"
else
    log_fail "Newly active pane dim flag wrong (expected '0', got '$FLAG2_AFTER')"
fi

# --- Test 3: Computed dim color is correct ---
echo ""
echo "Test 3: Dim background color is correctly computed"

# With terminal_bg=#1a1a1a (dark, lum=26) and opacity=0.8:
# gray target = 64,64,64 (dark theme)
# dim = round(26*0.8 + 64*0.2) = round(20.8 + 12.8) = round(33.6) = 34
# Expected: #222222
EXPECTED_DIM="bg=#222222"
ACTUAL_DIM=$(get_pane_style "$PANE1")

if [ "$ACTUAL_DIM" = "$EXPECTED_DIM" ]; then
    log_pass "Dim color matches expected: $EXPECTED_DIM"
else
    log_fail "Dim color mismatch (expected '$EXPECTED_DIM', got '$ACTUAL_DIM')"
fi

# --- Test 4: Border dimming applied ---
echo ""
echo "Test 4: Inactive border style is desaturated"

# Set an active border style first
tmux set-option -g pane-active-border-style "fg=#56949f"
kill -USR1 "$DAEMON_PID" 2>/dev/null
sleep 3

BORDER_STYLE=$(tmux show-options -gv pane-border-style 2>/dev/null || echo "")
if echo "$BORDER_STYLE" | grep -q "fg=#"; then
    log_pass "Inactive border has desaturated fg color: $BORDER_STYLE"
else
    log_fail "Inactive border style not set (got: '$BORDER_STYLE')"
fi

# --- Summary ---
echo ""
echo "========================================"
echo "Results: $TESTS_PASSED passed, $TESTS_FAILED failed out of $TESTS_RUN tests"
echo "========================================"

[ "$TESTS_FAILED" -eq 0 ]
