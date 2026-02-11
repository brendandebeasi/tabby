#!/usr/bin/env bash

set -eu

TABBY_TEST_SOCKET="${TABBY_TEST_SOCKET:-tabby-tests-visual}"
tmux() { command tmux -L "$TABBY_TEST_SOCKET" -f /dev/null "$@"; }

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
SCREENSHOT_DIR="$PROJECT_ROOT/tests/screenshots"
TEST_SESSION="tmux-tabs-visual-test"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

# Kill existing test session
tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true

# Create test session with various window configurations
log_info "Creating test session..."
tmux new-session -d -s "$TEST_SESSION" -n 'SD|frontend' -c "$HOME"
tmux set-option -t "$TEST_SESSION" @tmux_tabs_test 1

# Create various windows
tmux new-window -t "$TEST_SESSION" -n 'SD|backend'
tmux new-window -t "$TEST_SESSION" -n 'SD|database'
tmux new-window -t "$TEST_SESSION" -n 'GP|MSG|server'
tmux new-window -t "$TEST_SESSION" -n 'GP|Arsenal|build'
tmux new-window -t "$TEST_SESSION" -n 'GP|Ignite|deploy'
tmux new-window -t "$TEST_SESSION" -n 'notes'
tmux new-window -t "$TEST_SESSION" -n 'vim'
tmux new-window -t "$TEST_SESSION" -n 'htop'

# Set some windows with activity/bell flags for testing
tmux send-keys -t "$TEST_SESSION:SD|backend" "echo 'Activity test'" Enter
tmux set-option -t "$TEST_SESSION:SD|backend" monitor-activity on
tmux send-keys -t "$TEST_SESSION:GP|MSG|server" "echo -e '\\a'" Enter
tmux set-option -t "$TEST_SESSION:notes" monitor-silence 5

# Reload tmux config to ensure plugin is loaded
tmux source ~/.tmux.conf

log_info "Waiting for plugin to initialize..."
sleep 2

# Function to capture and convert tmux pane to image
capture_screenshot() {
    local name="$1"
    local desc="$2"
    
    log_info "Capturing: $desc"
    
    # Capture the entire tmux session
    tmux capture-pane -t "$TEST_SESSION" -e -p > "$SCREENSHOT_DIR/${name}.ansi"
    
    # Also capture as plain text for reference
    tmux capture-pane -t "$TEST_SESSION" -p > "$SCREENSHOT_DIR/${name}.txt"
    
    # If ansi2txt is available, create a colored HTML version
    if command -v ansi2html >/dev/null 2>&1; then
        ansi2html < "$SCREENSHOT_DIR/${name}.ansi" > "$SCREENSHOT_DIR/${name}.html"
    fi
    
    log_success "Captured $name"
}

# Test 1: Default view with horizontal tabs
capture_screenshot "01_horizontal_tabs" "Default horizontal tab bar"

# Test 2: Active window switching
tmux select-window -t "$TEST_SESSION:GP|MSG|server"
sleep 0.5
capture_screenshot "02_active_window" "Active window highlighting"

# Test 3: Sidebar open
tmux run-shell -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
sleep 1
capture_screenshot "03_sidebar_open" "Sidebar view"

# Test 4: Sidebar with different active window
tmux select-window -t "$TEST_SESSION:notes"
sleep 0.5
capture_screenshot "04_sidebar_active_change" "Sidebar with different active window"

# Test 5: Close sidebar
tmux run-shell -t "$TEST_SESSION" "$PROJECT_ROOT/scripts/toggle_sidebar.sh"
sleep 0.5
capture_screenshot "05_sidebar_closed" "Sidebar closed"

# Create index HTML file
log_info "Creating index.html..."
cat > "$SCREENSHOT_DIR/index.html" << 'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>tmux-tabs Visual Test Results</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 1200px;
            margin: 0 auto;
            padding: 20px;
            background: #1e1e1e;
            color: #d4d4d4;
        }
        h1 { color: #569cd6; }
        h2 { color: #4ec9b0; margin-top: 40px; }
        .test {
            margin: 20px 0;
            padding: 20px;
            background: #2d2d30;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.3);
        }
        .test h3 {
            color: #dcdcaa;
            margin-top: 0;
        }
        pre {
            background: #1e1e1e;
            padding: 15px;
            border-radius: 4px;
            overflow-x: auto;
            font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
            font-size: 14px;
            line-height: 1.5;
            border: 1px solid #383838;
        }
        .description {
            color: #969696;
            font-style: italic;
            margin-bottom: 10px;
        }
        iframe {
            width: 100%;
            height: 400px;
            border: 1px solid #383838;
            border-radius: 4px;
            background: #000;
        }
    </style>
</head>
<body>
    <h1>tmux-tabs Visual Test Results</h1>
    <p>Generated: $(date)</p>
    
    <h2>Test Screenshots</h2>
EOF

# Add each test result
for file in "$SCREENSHOT_DIR"/*.txt; do
    if [ -f "$file" ]; then
        basename="${file##*/}"
        name="${basename%.txt}"
        
        # Extract description from name
        desc=$(echo "$name" | sed 's/_/ /g' | sed 's/^[0-9]* //')
        
        cat >> "$SCREENSHOT_DIR/index.html" << EOF
    <div class="test">
        <h3>$desc</h3>
        <div class="description">File: $name</div>
EOF
        
        # If HTML version exists, embed it
        if [ -f "$SCREENSHOT_DIR/${name}.html" ]; then
            echo "        <iframe src=\"${name}.html\"></iframe>" >> "$SCREENSHOT_DIR/index.html"
        fi
        
        # Always show text version
        echo "        <pre>" >> "$SCREENSHOT_DIR/index.html"
        cat "$file" >> "$SCREENSHOT_DIR/index.html"
        echo "        </pre>" >> "$SCREENSHOT_DIR/index.html"
        echo "    </div>" >> "$SCREENSHOT_DIR/index.html"
    fi
done

cat >> "$SCREENSHOT_DIR/index.html" << 'EOF'
</body>
</html>
EOF

log_success "Visual tests complete!"
log_info "Results saved to: $SCREENSHOT_DIR"
log_info "Open $SCREENSHOT_DIR/index.html to view results"

# Cleanup
tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
