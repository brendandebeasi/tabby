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

# Auto-renumber windows when one is closed (keeps indices sequential)
tmux set-option -g renumber-windows on

# Enable activity monitoring for indicators
tmux set-option -g monitor-activity on
tmux set-option -g monitor-bell on

# New panes/windows open in the current pane's directory
tmux bind-key '"' split-window -v -c "#{pane_current_path}"
tmux bind-key '%' split-window -h -c "#{pane_current_path}"
tmux bind-key 'c' new-window -c "#{pane_current_path}"

# Enable automatic window renaming by default (shows running command/SSH host)
# Windows with group prefixes or manual names get locked via @tabby_locked
tmux set-option -g automatic-rename on
tmux set-option -g allow-rename on
tmux set-option -g automatic-rename-format '#{pane_current_command}'

# Read pane header colors from config (with defaults)
# Use exact match with leading spaces to avoid substring matches
PANE_ACTIVE_FG=$(grep -A10 "^pane_header:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  active_fg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_ACTIVE_BG=$(grep -A10 "^pane_header:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  active_bg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_INACTIVE_FG=$(grep -A10 "^pane_header:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  inactive_fg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_INACTIVE_BG=$(grep -A10 "^pane_header:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  inactive_bg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_COMMAND_FG=$(grep -A10 "^pane_header:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  command_fg:" | awk '{print $2}' | tr -d '"' || echo "")

# Apply defaults if not set
PANE_ACTIVE_FG=${PANE_ACTIVE_FG:-#ffffff}
PANE_ACTIVE_BG=${PANE_ACTIVE_BG:-#3498db}
PANE_INACTIVE_FG=${PANE_INACTIVE_FG:-#cccccc}
PANE_INACTIVE_BG=${PANE_INACTIVE_BG:-#333333}
PANE_COMMAND_FG=${PANE_COMMAND_FG:-#aaaaaa}

# Set global tmux options for pane header colors
tmux set-option -g @tabby_pane_active_fg "$PANE_ACTIVE_FG"
tmux set-option -g @tabby_pane_active_bg_default "$PANE_ACTIVE_BG"
tmux set-option -g @tabby_pane_inactive_fg "$PANE_INACTIVE_FG"
tmux set-option -g @tabby_pane_inactive_bg_default "$PANE_INACTIVE_BG"
tmux set-option -g @tabby_pane_command_fg "$PANE_COMMAND_FG"

# Read prompt style colors from config (with defaults)
PROMPT_FG=$(grep -A5 "^prompt:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  fg:" | awk '{print $2}' | tr -d '"' || echo "")
PROMPT_BG=$(grep -A5 "^prompt:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  bg:" | awk '{print $2}' | tr -d '"' || echo "")
PROMPT_BOLD=$(grep -A5 "^prompt:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "^  bold:" | awk '{print $2}' || echo "")

# Apply defaults if not set - black text on light gray background for legibility
PROMPT_FG=${PROMPT_FG:-#000000}
PROMPT_BG=${PROMPT_BG:-#f0f0f0}
PROMPT_BOLD=${PROMPT_BOLD:-true}

# Build message-style string
PROMPT_STYLE="fg=$PROMPT_FG,bg=$PROMPT_BG"
if [[ "$PROMPT_BOLD" == "true" ]]; then
    PROMPT_STYLE="$PROMPT_STYLE,bold"
fi

# Apply message-style for command prompts (rename, etc.)
tmux set-option -g message-style "$PROMPT_STYLE"

# Pane border styling - colored headers with info
tmux set-option -g pane-border-status top
tmux set-option -g pane-border-lines single
tmux set-option -g pane-border-style "fg=#444444"
tmux set-option -g pane-active-border-style "fg=$PANE_ACTIVE_BG"

# Pane header format: hide for utility panes (sidebar, pane-bar, tabbar)
# Shows window.pane number, title and command - right-click border for pane actions menu
# Uses @tabby_pane_active and @tabby_pane_inactive for per-window dynamic coloring
tmux set-option -g pane-border-format "#{?#{||:#{||:#{==:#{pane_current_command},sidebar},#{==:#{pane_current_command},pane-bar}},#{==:#{pane_current_command},tabbar}},,#{?pane_active,#[fg=#{@tabby_pane_active_fg}#,bg=#{?#{@tabby_pane_active},#{@tabby_pane_active},#{@tabby_pane_active_bg_default}}#,bold],#[fg=#{@tabby_pane_inactive_fg}#,bg=#{?#{@tabby_pane_inactive},#{@tabby_pane_inactive},#{@tabby_pane_inactive_bg_default}}]} #{window_index}.#{pane_index} #{pane_title} #[fg=#{@tabby_pane_command_fg}]#{pane_current_command} }"

# Right-click on pane border shows context menu (left-click+drag resizes panes)
tmux unbind-key -T root MouseDown1Border 2>/dev/null || true
tmux bind-key -T root MouseDown3Border display-menu -T "Pane Actions" -x M -y M \
    "Split Vertical" "|" "split-window -h -c '#{pane_current_path}'" \
    "Split Horizontal" "-" "split-window -v -c '#{pane_current_path}'" \
    "" \
    "Break to New Window" "b" "break-pane" \
    "Swap Up" "u" "swap-pane -U" \
    "Swap Down" "d" "swap-pane -D" \
    "" \
    "Kill Pane" "x" "kill-pane"


# Terminal title configuration
# Read from config.yaml or use defaults
TITLE_ENABLED=$(grep -A2 "^terminal_title:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "enabled:" | awk '{print $2}' || echo "true")
TITLE_FORMAT=$(grep -A2 "^terminal_title:" "$CURRENT_DIR/config.yaml" 2>/dev/null | grep "format:" | sed 's/.*format: *"\([^"]*\)".*/\1/' || echo "tmux #{window_index}.#{pane_index} #{window_name} #{pane_current_command}")

if [[ "$TITLE_ENABLED" != "false" ]]; then
    tmux set-option -g set-titles on
    tmux set-option -g set-titles-string "$TITLE_FORMAT"
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

    # Only enable status bar if sidebar mode is NOT active
    SIDEBAR_STATE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
    if [ "$SIDEBAR_STATE" != "enabled" ]; then
        tmux set-option -g status on
    fi
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
# Sends SIGUSR1 to sidebar process via PID file
SIGNAL_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/signal_sidebar.sh"

# Create the signal helper script
cat > "$SIGNAL_SIDEBAR_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Signal sidebar to refresh window list
SESSION_ID=$(tmux display-message -p '#{session_id}')
PID_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.pid"

if [ -f "$PID_FILE" ]; then
    SIDEBAR_PID=$(cat "$PID_FILE")
    if [ -n "$SIDEBAR_PID" ] && kill -0 "$SIDEBAR_PID" 2>/dev/null; then
        # Send SIGUSR1 to trigger refresh
        kill -USR1 "$SIDEBAR_PID" 2>/dev/null || true
    fi
fi
SCRIPT_EOF
chmod +x "$SIGNAL_SIDEBAR_SCRIPT"

# Helper script to refresh status bar
REFRESH_STATUS_SCRIPT="$CURRENT_DIR/scripts/refresh_status.sh"

# Helper script to ensure sidebar persistence
ENSURE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/ensure_sidebar.sh"

# Helper script to restore sidebar on session reattach
RESTORE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/restore_sidebar.sh"
chmod +x "$RESTORE_SIDEBAR_SCRIPT"

# Helper script to update pane bar (horizontal mode second line)
UPDATE_PANE_BAR_SCRIPT="$CURRENT_DIR/scripts/update_pane_bar.sh"
chmod +x "$UPDATE_PANE_BAR_SCRIPT"

# Helper scripts for clickable pane-bar TUI
ENSURE_PANE_BAR_SCRIPT="$CURRENT_DIR/scripts/ensure_pane_bar.sh"
chmod +x "$ENSURE_PANE_BAR_SCRIPT"
SIGNAL_PANE_BAR_SCRIPT="$CURRENT_DIR/scripts/signal_pane_bar.sh"
chmod +x "$SIGNAL_PANE_BAR_SCRIPT"

# Set up hooks for window events
# These hooks trigger both sidebar and status bar refresh
tmux set-hook -g window-linked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
# Lock window name on manual rename (disable automatic-rename for that window)
tmux set-hook -g window-renamed "run-shell 'tmux set-window-option -t \"#{window_id}\" automatic-rename off'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
tmux set-hook -g window-unlinked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
tmux set-hook -g after-new-window "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'"
# Combined script to reduce latency
ON_WINDOW_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_window_select.sh"
chmod +x "$ON_WINDOW_SELECT_SCRIPT"
tmux set-hook -g after-select-window "run-shell '$ON_WINDOW_SELECT_SCRIPT'"
tmux set-hook -g after-rename-window "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"

# Refresh sidebar and pane bar when pane focus changes
ON_PANE_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_pane_select.sh"
chmod +x "$ON_PANE_SELECT_SCRIPT"
tmux set-hook -g after-select-pane "run-shell '$ON_PANE_SELECT_SCRIPT'"
tmux set-hook -g pane-focus-in "run-shell '$ON_PANE_SELECT_SCRIPT'"

# Update pane bar when panes are split, and preserve group prefixes
PRESERVE_NAME_SCRIPT="$CURRENT_DIR/scripts/preserve_window_name.sh"
chmod +x "$PRESERVE_NAME_SCRIPT"
tmux set-hook -g after-split-window "run-shell '$PRESERVE_NAME_SCRIPT'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_PANE_BAR_SCRIPT'"

# Close window if only sidebar/tabbar remains after main pane exits
CLEANUP_SCRIPT="$CURRENT_DIR/scripts/cleanup_orphan_sidebar.sh"
chmod +x "$CLEANUP_SCRIPT"
tmux set-hook -g pane-exited "run-shell '$CLEANUP_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_PANE_BAR_SCRIPT'"

# Restore sidebar when client reattaches to session
tmux set-hook -g client-attached "run-shell '$RESTORE_SIDEBAR_SCRIPT'"

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
