#!/usr/bin/env bash
# Integration test script for Tabby UI features
# Run from project root: bash tests/test_ui_features.sh

set -e

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-ui}"
tmux() { command tmux -L "$TABBY_TEST_SOCKET" -f /dev/null "$@"; }

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS_COUNT=0
FAIL_COUNT=0

pass() {
    echo -e "${GREEN}PASS${NC}: $1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    echo -e "${RED}FAIL${NC}: $1"
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

skip() {
    echo -e "${YELLOW}SKIP${NC}: $1"
}

# Check if running inside tmux
if [ -z "$TMUX" ]; then
    echo "Warning: Not running inside tmux. Some tests will be skipped."
    IN_TMUX=false
else
    IN_TMUX=true
fi

echo "=========================================="
echo "Tabby UI Features Integration Test"
echo "=========================================="
echo ""

# Test 1: Build binaries
echo "--- Test: Build Binaries ---"
if go build -o bin/tabby-daemon ./cmd/tabby-daemon/ 2>&1; then
    pass "tabby-daemon builds successfully"
else
    fail "tabby-daemon build failed"
fi

if go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/ 2>&1; then
    pass "sidebar-renderer builds successfully"
else
    fail "sidebar-renderer build failed"
fi

if go build -o bin/pane-header ./cmd/pane-header/ 2>&1; then
    pass "pane-header builds successfully"
else
    fail "pane-header build failed"
fi

# Test 2: Shell script syntax validation
echo ""
echo "--- Test: Shell Script Syntax ---"
for script in scripts/*.sh; do
    if [ -f "$script" ]; then
        if bash -n "$script" 2>/dev/null; then
            pass "$(basename "$script") syntax valid"
        else
            fail "$(basename "$script") has syntax errors"
        fi
    fi
done

# Test 3: Debounce file mechanism in ensure_sidebar.sh
echo ""
echo "--- Test: Debounce Mechanism ---"
if grep -q "DEBOUNCE_MS=100" scripts/ensure_sidebar.sh; then
    pass "ensure_sidebar.sh has 100ms debounce"
else
    fail "ensure_sidebar.sh missing debounce"
fi

# Test 4: MouseDown3Pane unbinding for context menus
echo ""
echo "--- Test: Context Menu Support ---"
if grep -q "unbind.*MouseDown3Pane" tabby.tmux; then
    pass "MouseDown3Pane is unbound for context menu support"
else
    fail "MouseDown3Pane not unbound - right-click menus may not work"
fi

# Test 5: Collapse functionality exists
echo ""
echo "--- Test: Collapse/Expand Functionality ---"
if grep -q "@tabby_collapsed" cmd/tabby-daemon/coordinator.go; then
    pass "Window collapse state tracking exists"
else
    fail "Window collapse state tracking missing"
fi

if grep -q "@tabby_pane_collapsed" cmd/tabby-daemon/coordinator.go; then
    pass "Pane collapse state tracking exists"
else
    fail "Pane collapse state tracking missing"
fi

if grep -q "toggle_pane_collapse" cmd/tabby-daemon/coordinator.go; then
    pass "Pane collapse toggle action exists"
else
    fail "Pane collapse toggle action missing"
fi

# Test 6: Border color functionality
echo ""
echo "--- Test: Border Color Functionality ---"
if grep -q "pane-border-style" scripts/on_window_select.sh; then
    pass "Border style update exists in on_window_select.sh"
else
    fail "Border style update missing"
fi

if grep -q "@tabby_pane_active" scripts/on_window_select.sh; then
    pass "Active pane color tracking exists"
else
    fail "Active pane color tracking missing"
fi

# Test 7: Go tests
echo ""
echo "--- Test: Go Unit Tests ---"
if go test ./pkg/grouping/ 2>/dev/null; then
    pass "pkg/grouping tests pass"
else
    fail "pkg/grouping tests failed"
fi

if go test ./pkg/colors/ 2>/dev/null; then
    pass "pkg/colors tests pass"
else
    fail "pkg/colors tests failed"
fi

# Test 8: Documentation
echo ""
echo "--- Test: Documentation ---"
if grep -qi "collapse" README.md; then
    pass "README documents collapse functionality"
else
    fail "README missing collapse documentation"
fi

if grep -qi "right.*click\|context.*menu" README.md; then
    pass "README documents context menus"
else
    fail "README missing context menu documentation"
fi

# Tmux-specific tests (only if inside tmux)
if [ "$IN_TMUX" = true ]; then
    echo ""
    echo "--- Test: Tmux Integration ---"

    # Test collapse option can be set
    if tmux set-option -gq @tabby_test_collapse 1 2>/dev/null; then
        tmux set-option -gqu @tabby_test_collapse 2>/dev/null
        pass "Tmux options can be set"
    else
        fail "Cannot set tmux options"
    fi
else
    skip "Tmux integration tests (not running inside tmux)"
fi

# Summary
echo ""
echo "=========================================="
echo "Test Summary"
echo "=========================================="
echo -e "Passed: ${GREEN}$PASS_COUNT${NC}"
echo -e "Failed: ${RED}$FAIL_COUNT${NC}"
echo ""

if [ "$FAIL_COUNT" -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi
