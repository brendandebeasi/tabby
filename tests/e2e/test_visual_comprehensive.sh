#!/usr/bin/env bash
set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-visual}"
tmux() { command tmux -L "$TABBY_TEST_SOCKET" -f /dev/null "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"

echo "=========================================="
echo "Comprehensive Visual Test Suite"
echo "=========================================="

setup_test_env() {
    echo "Building binaries..."
    cd "$PROJECT_ROOT"
    go build -o bin/render-status cmd/render-status/main.go
    go build -o bin/sidebar-renderer cmd/sidebar-renderer/main.go
    echo "✓ Binaries built"
    
    echo "Reloading tmux configuration..."
    tmux run-shell "$PROJECT_ROOT/tabby.tmux"
    echo "✓ Configuration reloaded"
}

test_horizontal_tabs() {
    echo ""
    echo "=== HORIZONTAL TABS TEST ==="
    
    echo "1. Creating test windows..."
    tmux new-window -n "SD|frontend" -t 10
    tmux new-window -n "SD|backend" -t 11
    tmux new-window -n "GP|MSG|service" -t 12
    tmux new-window -n "GP|Arsenal|deploy" -t 13
    tmux new-window -n "notes" -t 14
    tmux new-window -n "vim" -t 15
    sleep 0.5
    
    echo "2. Testing render-status output..."
    "$PROJECT_ROOT/bin/render-status"
    echo ""
    
    echo "3. Testing window switching..."
    tmux select-window -t 10
    sleep 0.2
    echo "   - Selected SD|frontend"
    
    tmux select-window -t 13
    sleep 0.2
    echo "   - Selected GP|Arsenal|deploy"
    
    echo "4. Testing window rename..."
    tmux rename-window -t 14 "documentation"
    sleep 0.5
    echo "   - Renamed 'notes' to 'documentation'"
    "$PROJECT_ROOT/bin/render-status"
    echo ""
    
    echo "5. Testing window kill..."
    tmux kill-window -t 11
    sleep 0.5
    echo "   - Killed SD|backend"
    "$PROJECT_ROOT/bin/render-status"
    echo ""
    
    echo "6. Testing activity indicators..."
    tmux send-keys -t "SD|frontend" "echo 'activity test'" C-m
    sleep 0.5
    tmux select-window -t 0
    sleep 0.2
    echo "   - Triggered activity in SD|frontend"
    "$PROJECT_ROOT/bin/render-status"
    echo ""
}

test_vertical_sidebar() {
    echo ""
    echo "=== VERTICAL SIDEBAR TEST ==="
    
    echo "1. Opening sidebar..."
    "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
    sleep 1
    
    SIDEBAR_PANE=$(tmux list-panes -F "#{pane_current_command}|#{pane_id}" | grep -E "^(sidebar|sidebar-renderer)\|" | cut -d'|' -f2)
    
    if [ -n "$SIDEBAR_PANE" ]; then
        echo "✓ Sidebar opened (pane: $SIDEBAR_PANE)"
        
        echo "2. Capturing sidebar content..."
        tmux capture-pane -t "$SIDEBAR_PANE" -p | head -20
        echo ""
        
        echo "3. Testing keyboard navigation..."
        echo "   - Sending 'j' (down)..."
        tmux send-keys -t "$SIDEBAR_PANE" "j"
        sleep 0.2
        
        echo "   - Sending 'j' (down) again..."
        tmux send-keys -t "$SIDEBAR_PANE" "j"
        sleep 0.2
        
        echo "   - Sending 'k' (up)..."
        tmux send-keys -t "$SIDEBAR_PANE" "k"
        sleep 0.2
        
        echo "4. Testing window selection..."
        echo "   - Sending Enter to select window..."
        tmux send-keys -t "$SIDEBAR_PANE" "Enter"
        sleep 0.5
        
        CURRENT_WINDOW=$(tmux display-message -p "#{window_index}:#{window_name}")
        echo "   - Current window: $CURRENT_WINDOW"
        
        echo "5. Testing ESC to return focus..."
        tmux select-pane -t "$SIDEBAR_PANE"
        tmux send-keys -t "$SIDEBAR_PANE" "Escape"
        sleep 0.2
        
        ACTIVE_PANE=$(tmux display-message -p "#{pane_id}")
        if [ "$ACTIVE_PANE" != "$SIDEBAR_PANE" ]; then
            echo "✓ ESC successfully returned focus to main pane"
        else
            echo "✗ ESC did not return focus"
        fi
        
        echo "6. Testing sidebar refresh on window changes..."
        tmux new-window -n "test-refresh"
        sleep 0.5
        echo "   - Created new window, capturing sidebar..."
        tmux capture-pane -t "$SIDEBAR_PANE" -p | grep -E "test-refresh|Close Tab" | head -5
        
        tmux kill-window -t "test-refresh"
        sleep 0.5
        echo "   - Killed window, sidebar should update"
        
        echo "7. Closing sidebar..."
        "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
        sleep 0.5
        
        if ! tmux list-panes -F "#{pane_current_command}" | grep -Eq "^(sidebar|sidebar-renderer)$"; then
            echo "✓ Sidebar closed successfully"
        else
            echo "✗ Sidebar still open"
        fi
    else
        echo "✗ Failed to open sidebar"
    fi
}

test_mouse_interaction() {
    echo ""
    echo "=== MOUSE INTERACTION TEST ==="
    echo "Note: Manual testing required for mouse clicks"
    echo ""
    echo "Horizontal tabs (if supported):"
    echo "  - Click on any tab to switch windows"
    echo ""
    echo "Sidebar mouse features:"
    echo "  1. Click on window to select"
    echo "  2. Middle-click to close window"
    echo "  3. Right-click for context menu"
    echo "  4. Click [+] New Tab button"
    echo "  5. Click [x] Close Tab button"
}

cleanup_test_windows() {
    echo ""
    echo "Cleaning up test windows..."
    for i in {10..15}; do
        tmux kill-window -t "$i" 2>/dev/null || true
    done
    echo "✓ Cleanup complete"
}

main() {
    setup_test_env
    
    test_horizontal_tabs
    test_vertical_sidebar
    test_mouse_interaction
    
    cleanup_test_windows
    
    echo ""
    echo "=========================================="
    echo "Visual tests completed!"
    echo "Check the output above for any issues."
    echo "=========================================="
}

main
