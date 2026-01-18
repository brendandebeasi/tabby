#!/usr/bin/env bash
# tmux-tabs plugin entry point
# Fixes: BUG-003 (hook signal targeting)

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check if test mode is enabled (gated feature)
TEST_MODE=$(tmux show-option -gqv "@tmux_tabs_test")
if [ "$TEST_MODE" != "1" ]; then
    exit 0
fi

# Build binaries if not present
if [ ! -f "$CURRENT_DIR/bin/render-status" ]; then
    "$CURRENT_DIR/scripts/install.sh" || true
fi

# Read configuration
POSITION=$(grep "^position:" "$CURRENT_DIR/config.yaml" 2>/dev/null | awk '{print $2}' || echo "top")
POSITION=${POSITION:-top}

# Configure horizontal status bar
if [[ "$POSITION" == "top" ]] || [[ "$POSITION" == "bottom" ]]; then
    # Clear any existing status-format settings that would override window-status
    for i in {0..10}; do
        tmux set-option -gu status-format[$i] 2>/dev/null || true
    done
    tmux set-option -gu status-format 2>/dev/null || true
    
    tmux set-option -g status on
    tmux set-option -g status-position "$POSITION"
    tmux set-option -g status-interval 1
    tmux set-option -g status-style "bg=default"
    tmux set-option -g status-justify left
    
    # Use hybrid approach for clickable tabs
    tmux set-option -g status-left ""
    tmux set-option -g status-left-length 0
    
    tmux set-option -g status-right "#[fg=#27ae60,bold][+] "
    tmux set-option -g status-right-length 20
    
    # Window status formats with custom rendering
    tmux set-window-option -g window-status-style "fg=default,bg=default"
    tmux set-window-option -g window-status-current-style "fg=default,bg=default"
    
    tmux set-window-option -g window-status-format "#($CURRENT_DIR/bin/render-tab normal #I '#W' '#{window_flags}')"
    tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/bin/render-tab active #I '#W' '#{window_flags}')"
    
    tmux set-window-option -g window-status-separator ""
    
    # Mouse bindings for tabs
    tmux set-option -g mouse on
    tmux bind-key -T root MouseDown1Status select-window -t =
    tmux bind-key -T root MouseDown2Status kill-window
    tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%'"
    tmux bind-key -T root MouseDown1StatusRight new-window
fi

# Helper script to signal sidebar refresh
# Uses session-based state file instead of broken $$ PID
SIGNAL_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/signal_sidebar.sh"

# Create the signal helper script
cat > "$SIGNAL_SIDEBAR_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Signal sidebar to refresh window list
SESSION_ID=$(tmux display-message -p '#{session_id}')
STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"

if [ -f "$STATE_FILE" ]; then
    SIDEBAR_PANE=$(cat "$STATE_FILE")
    if [ -n "$SIDEBAR_PANE" ]; then
        # Send refresh signal to sidebar pane via tmux
        # The sidebar binary listens for SIGUSR1
        tmux send-keys -t "$SIDEBAR_PANE" "" 2>/dev/null || true
    fi
fi
SCRIPT_EOF
chmod +x "$SIGNAL_SIDEBAR_SCRIPT"

# Helper script to refresh status bar
REFRESH_STATUS_SCRIPT="$CURRENT_DIR/scripts/refresh_status.sh"

# Helper script to ensure sidebar persistence
ENSURE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/ensure_sidebar.sh"

# Set up hooks for window events
# These hooks trigger both sidebar and status bar refresh
tmux set-hook -g window-linked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'"
tmux set-hook -g window-renamed "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
tmux set-hook -g window-unlinked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
tmux set-hook -g after-new-window "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'"
tmux set-hook -g after-select-window "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'; run-shell 'tmux refresh-client -S'"
tmux set-hook -g after-rename-window "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"

# Configure sidebar toggle keybinding
TOGGLE_KEY=$(grep "toggle_sidebar:" "$CURRENT_DIR/config.yaml" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "prefix + Tab")
KEY=${TOGGLE_KEY##*+ }
if [ -z "$KEY" ]; then KEY="Tab"; fi

tmux bind-key "$KEY" run-shell "$CURRENT_DIR/scripts/toggle_sidebar.sh"

# Optional: Also bind to a prefix-less key for quick access
# tmux bind-key -n M-Tab run-shell "$CURRENT_DIR/scripts/toggle_sidebar.sh"

# Mode toggle and switching
tmux bind-key M run-shell "$CURRENT_DIR/scripts/toggle_mode.sh"
tmux bind-key V run-shell "$CURRENT_DIR/scripts/switch_to_vertical.sh" 
tmux bind-key H run-shell "$CURRENT_DIR/scripts/switch_to_horizontal.sh"

# Set up keyboard shortcuts for tab navigation
tmux bind-key -n M-h previous-window
tmux bind-key -n M-l next-window
tmux bind-key -n M-n new-window
tmux bind-key -n M-x kill-pane
tmux bind-key -n M-q display-panes

# Number key bindings for direct window access
tmux bind-key -n M-1 select-window -t :=0
tmux bind-key -n M-2 select-window -t :=1
tmux bind-key -n M-3 select-window -t :=2
tmux bind-key -n M-4 select-window -t :=3
tmux bind-key -n M-5 select-window -t :=4
tmux bind-key -n M-6 select-window -t :=5
tmux bind-key -n M-7 select-window -t :=6
tmux bind-key -n M-8 select-window -t :=7
tmux bind-key -n M-9 select-window -t :=8
tmux bind-key -n M-0 select-window -t :=9
