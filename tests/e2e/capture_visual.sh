#!/usr/bin/env bash
# Visual Capture Script for tabby
# Captures terminal output with ANSI codes for visual regression testing

set -euo pipefail

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-visual}"
tmux() { command tmux -L "$TABBY_TEST_SOCKET" -f /dev/null "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
SCREENSHOT_DIR="$PROJECT_ROOT/tests/screenshots"
TEST_SESSION="tabby-visual-test"

mkdir -p "$SCREENSHOT_DIR"/{baseline,current,diffs}

log_info() { echo "[INFO] $1"; }

# ============================================================================
# Setup
# ============================================================================

setup_visual_session() {
    log_info "Setting up visual test session"
    
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
    
    tmux new-session -d -s "$TEST_SESSION" -n "SD|app"
    tmux set-option -t "$TEST_SESSION" allow-rename off
    tmux set-option -t "$TEST_SESSION" automatic-rename off
    tmux set-option -g @tabby_test 1
    
    tmux new-window -t "$TEST_SESSION" -n "SD|debug"
    tmux new-window -t "$TEST_SESSION" -n "GP|MSG|chat"
    tmux new-window -t "$TEST_SESSION" -n "GP|Arsenal|build"
    tmux new-window -t "$TEST_SESSION" -n "notes"
    tmux new-window -t "$TEST_SESSION" -n "vim"
    
    tmux select-window -t "$TEST_SESSION:0"
}

cleanup() {
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
}

trap cleanup EXIT

# ============================================================================
# Capture Functions
# ============================================================================

capture_horizontal_tabs() {
    log_info "Capturing: Horizontal 3 Groups"
    
    # Capture render-status output with ANSI codes
    "$PROJECT_ROOT/bin/render-status" > "$SCREENSHOT_DIR/current/horizontal-3-groups.txt" 2>/dev/null || true
    
    # Convert to HTML if ansi2html is available
    if command -v ansi2html &>/dev/null; then
        cat "$SCREENSHOT_DIR/current/horizontal-3-groups.txt" | ansi2html --partial > "$SCREENSHOT_DIR/current/horizontal-3-groups.html"
        log_info "  HTML output created"
    fi
    
    # Also capture as JSON for programmatic comparison
    cat > "$SCREENSHOT_DIR/current/horizontal-3-groups.json" <<EOF
{
  "capture_time": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "session": "$TEST_SESSION",
  "windows": [
$(tmux list-windows -t "$TEST_SESSION" -F '    {"index": #{window_index}, "name": "#{window_name}", "active": #{window_active}}' | paste -sd',' -)
  ],
  "raw_output": $(cat "$SCREENSHOT_DIR/current/horizontal-3-groups.txt" | jq -Rs .)
}
EOF
    
    log_info "  Captured horizontal tabs"
}

capture_sidebar_open() {
    log_info "Capturing: Sidebar Open"
    
    # Toggle sidebar open
    tmux run-shell -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh" 2>/dev/null || true
    sleep 1
    
    # Find sidebar pane
    local sidebar_pane
    sidebar_pane=$(tmux list-panes -s -t "$TEST_SESSION" -F "#{pane_current_command}|#{pane_id}" | grep -E "^(sidebar|sidebar-renderer)\|" | cut -d'|' -f2 | head -1)
    
    if [ -n "$sidebar_pane" ]; then
        # Capture sidebar pane content
        tmux capture-pane -t "$sidebar_pane" -e -p > "$SCREENSHOT_DIR/current/sidebar-open.txt"
        
        if command -v ansi2html &>/dev/null; then
            cat "$SCREENSHOT_DIR/current/sidebar-open.txt" | ansi2html --partial > "$SCREENSHOT_DIR/current/sidebar-open.html"
        fi
        
        log_info "  Captured sidebar pane: $sidebar_pane"
    else
        echo "ERROR: Sidebar not found" > "$SCREENSHOT_DIR/current/sidebar-open.txt"
        log_info "  WARNING: Could not find sidebar pane"
    fi
}

capture_with_activity() {
    log_info "Capturing: Activity Indicator"
    
    # Trigger activity in a background window
    tmux send-keys -t "$TEST_SESSION:notes" "echo 'Activity triggered'" Enter
    sleep 0.5
    
    # Select different window to make notes a background window
    tmux select-window -t "$TEST_SESSION:0"
    sleep 0.3
    
    # Capture with potential activity flag
    "$PROJECT_ROOT/bin/render-status" > "$SCREENSHOT_DIR/current/with-activity.txt" 2>/dev/null || true
    
    log_info "  Captured activity state"
}

# ============================================================================
# Comparison Functions
# ============================================================================

compare_captures() {
    log_info "Comparing captures with baseline"
    
    local has_diff=0
    
    for file in "$SCREENSHOT_DIR/current"/*.txt; do
        local basename
        basename=$(basename "$file")
        local baseline="$SCREENSHOT_DIR/baseline/$basename"
        local diff_file="$SCREENSHOT_DIR/diffs/${basename%.txt}.diff"
        
        if [ -f "$baseline" ]; then
            if diff -u "$baseline" "$file" > "$diff_file" 2>/dev/null; then
                log_info "  $basename: MATCH"
                rm -f "$diff_file"
            else
                log_info "  $basename: DIFFERS (see $diff_file)"
                has_diff=1
            fi
        else
            log_info "  $basename: NO BASELINE (run with --update-baseline)"
        fi
    done
    
    return $has_diff
}

update_baseline() {
    log_info "Updating baseline captures"
    
    cp "$SCREENSHOT_DIR/current"/*.txt "$SCREENSHOT_DIR/baseline/" 2>/dev/null || true
    cp "$SCREENSHOT_DIR/current"/*.html "$SCREENSHOT_DIR/baseline/" 2>/dev/null || true
    cp "$SCREENSHOT_DIR/current"/*.json "$SCREENSHOT_DIR/baseline/" 2>/dev/null || true
    
    log_info "Baseline updated"
}

# ============================================================================
# Main
# ============================================================================

main() {
    local update_baseline_flag=0
    
    for arg in "$@"; do
        case "$arg" in
            --update-baseline) update_baseline_flag=1 ;;
        esac
    done
    
    log_info "========================================"
    log_info "Tabby Visual Capture"
    log_info "========================================"
    
    # Build first
    log_info "Building binaries..."
    (cd "$PROJECT_ROOT" && go build -o bin/render-status cmd/render-status/main.go) || exit 1
    (cd "$PROJECT_ROOT" && go build -o bin/sidebar-renderer cmd/sidebar-renderer/main.go) || exit 1
    
    setup_visual_session
    
    capture_horizontal_tabs
    capture_sidebar_open
    capture_with_activity
    
    if [ "$update_baseline_flag" -eq 1 ]; then
        update_baseline
    else
        compare_captures || log_info "Some captures differ from baseline"
    fi
    
    log_info "========================================"
    log_info "Captures saved to: $SCREENSHOT_DIR/current/"
    log_info "========================================"
}

main "$@"
