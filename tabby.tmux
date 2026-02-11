#!/usr/bin/env bash
# tmux-tabs plugin entry point
# Fixes: BUG-003 (hook signal targeting)

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Resolve config path via XDG helper (never read $CURRENT_DIR/config.yaml directly)
source "$CURRENT_DIR/scripts/_config_path.sh"
CONFIG_FILE="$TABBY_CONFIG_FILE"

# Optional kill-switch for troubleshooting.
# Tabby is enabled by default unless explicitly disabled.
TABBY_ENABLED=$(tmux show-option -gqv "@tabby_enabled")
if [ "$TABBY_ENABLED" = "0" ]; then
    exit 0
fi

# Build binaries if not present
if [ ! -f "$CURRENT_DIR/bin/render-status" ]; then
    "$CURRENT_DIR/scripts/install.sh" || true
fi

# Start local-only Tabby Web bridge if enabled
WEB_ENABLED=$(grep -A6 "^web:" "$CONFIG_FILE" 2>/dev/null | grep "enabled:" | awk '{print $2}' | tr -d '"' || echo "false")
WEB_ENABLED=${WEB_ENABLED:-false}
if [[ "$WEB_ENABLED" == "true" ]]; then
    WEB_START_SCRIPT="$CURRENT_DIR/scripts/start_web_bridge.sh"
    chmod +x "$WEB_START_SCRIPT"
    run-shell "$WEB_START_SCRIPT"
    tmux set-hook -g session-created "run-shell '$WEB_START_SCRIPT'"
    tmux set-hook -g client-attached "run-shell '$WEB_START_SCRIPT'"
fi

# Auto-renumber windows when one is closed (keeps indices sequential)
tmux set-option -g renumber-windows on

# Bell monitoring for notifications (activity is too noisy - triggers on any output)
tmux set-option -g monitor-activity off
tmux set-option -g monitor-bell on
tmux set-option -g bell-action other  # Flag bells from non-active windows

# Window sizing: resize all windows/panes together when terminal resizes
tmux set-option -g window-size largest
tmux set-option -g aggressive-resize on

# New panes/windows open in the current pane's directory
tmux bind-key '"' split-window -v -c "#{pane_current_path}"
tmux bind-key '%' split-window -h -c "#{pane_current_path}"

# Create script to capture current window's group and create new window
NEW_WINDOW_SCRIPT="$CURRENT_DIR/scripts/new_window_with_group.sh"
cat > "$NEW_WINDOW_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Capture current window's group and create new window
CURRENT_GROUP=$(tmux show-window-options -v @tabby_group 2>/dev/null || echo "")
CURRENT_PATH=$(tmux display-message -p "#{pane_current_path}")
tmux set-option -g @tabby_new_window_group "$CURRENT_GROUP"
tmux new-window -c "$CURRENT_PATH"
SCRIPT_EOF
chmod +x "$NEW_WINDOW_SCRIPT"

# Override 'c' to capture group and create new window
tmux bind-key 'c' run-shell "$NEW_WINDOW_SCRIPT"

# Enable automatic window renaming by default (shows running command/SSH host)
# Windows with group prefixes or manual names get locked via @tabby_locked
tmux set-option -g automatic-rename on
tmux set-option -g allow-rename on
tmux set-option -g automatic-rename-format '#{pane_current_command}'

# Read pane header colors from config (with defaults)
# Use exact match with leading spaces to avoid substring matches
PANE_ACTIVE_FG=$(grep -A10 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "^  active_fg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_ACTIVE_BG=$(grep -A10 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "^  active_bg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_INACTIVE_FG=$(grep -A10 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "^  inactive_fg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_INACTIVE_BG=$(grep -A10 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "^  inactive_bg:" | awk '{print $2}' | tr -d '"' || echo "")
PANE_COMMAND_FG=$(grep -A10 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "^  command_fg:" | awk '{print $2}' | tr -d '"' || echo "")

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

# Read border_from_tab option (use tab color for active pane border)
BORDER_FROM_TAB=$(grep -A15 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "border_from_tab:" | awk '{print $2}' || echo "false")
BORDER_FROM_TAB=${BORDER_FROM_TAB:-false}
tmux set-option -g @tabby_border_from_tab "$BORDER_FROM_TAB"

# Read custom_border option - when enabled, we render our own border and hide tmux borders
CUSTOM_BORDER=$(grep -A20 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "custom_border:" | awk '{print $2}' || echo "false")
CUSTOM_BORDER=${CUSTOM_BORDER:-false}

# Read terminal_bg for hiding borders (user's terminal background color)
TERMINAL_BG=$(grep -A20 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "terminal_bg:" | awk '{print $2}' | tr -d '"' || echo "#000000")
TERMINAL_BG=${TERMINAL_BG:-#000000}

# Read border line style: single, double, heavy, simple, number
# When custom_border is enabled, force 'simple' (thinnest) to minimize visibility of tmux borders
if [[ "$CUSTOM_BORDER" == "true" ]]; then
    BORDER_LINES="simple"
    # Hide tmux borders by matching them to terminal background
    tmux set-option -g pane-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
    tmux set-option -g pane-active-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
else
    BORDER_LINES=$(grep -A15 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "border_lines:" | awk '{print $2}' || echo "single")
    BORDER_LINES=${BORDER_LINES:-single}
fi

# Read border foreground color
BORDER_FG=$(grep -A15 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "border_fg:" | awk '{print $2}' | tr -d '"' || echo "#444444")
BORDER_FG=${BORDER_FG:-#444444}

# Read prompt style colors from config (with defaults)
PROMPT_FG=$(grep -A5 "^prompt:" "$CONFIG_FILE" 2>/dev/null | grep "^  fg:" | awk '{print $2}' | tr -d '"' || echo "")
PROMPT_BG=$(grep -A5 "^prompt:" "$CONFIG_FILE" 2>/dev/null | grep "^  bg:" | awk '{print $2}' | tr -d '"' || echo "")
PROMPT_BOLD=$(grep -A5 "^prompt:" "$CONFIG_FILE" 2>/dev/null | grep "^  bold:" | awk '{print $2}' || echo "")

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

# Check if overlay pane headers are enabled (replaces native pane-border-status)
PANE_HEADERS=$(grep -A20 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "pane_headers:" | awk '{print $2}' || echo "false")
PANE_HEADERS=${PANE_HEADERS:-false}

if [[ "$PANE_HEADERS" == "true" ]]; then
    tmux set-option -g pane-border-status off
    tmux set-option -g @tabby_pane_headers on
else
    tmux set-option -g pane-border-status top
fi

# Pane border styling - colored headers with info
tmux set-option -g pane-border-lines "$BORDER_LINES"
# When custom_border is enabled, borders are hidden (set earlier)
# Otherwise, use the same color for both to prevent half/half on shared edges
if [[ "$CUSTOM_BORDER" != "true" ]]; then
    tmux set-option -g pane-border-style "fg=$PANE_ACTIVE_BG"
    tmux set-option -g pane-active-border-style "fg=$PANE_ACTIVE_BG"
fi

# Always use default pane backgrounds (no forced dimming)
tmux set-option -g window-style "default"
tmux set-option -g window-active-style "default"

# Pane header format: hide for utility panes (sidebar, pane-bar, tabbar)

# Unbind right-click on pane so it passes through to apps with mouse capture
# (sidebar-renderer / pane-header use BubbleTea mouse mode and handle right-click internally)
tmux unbind-key -T root MouseDown3Pane 2>/dev/null || true

# Handle clicks on pane-header panes specially to allow buttons to work regardless of focus
# Architecture: Only intercept pane-header clicks. Let sidebar and normal panes use default tmux behavior.
# Flow: MouseDown1Pane -> if pane-header: store click pos, select pane -> BubbleTea FocusMsg -> daemon handles action

# Create click handler script for pane-header clicks only
CLICK_HANDLER_SCRIPT="$CURRENT_DIR/scripts/pane_click_handler.sh"
cat > "$CLICK_HANDLER_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Handle click on pane-header panes. Store click position and select pane to trigger FocusMsg.
# The pane-header's BubbleTea receives FocusMsg, reads stored position, sends to daemon.
# Daemon handles all button logic (single source of truth).
PANE_ID="$1"
MOUSE_X="$2"
MOUSE_Y="$3"

# Store click position for pane-header to read on focus gain
tmux set-option -g @tabby_last_click_x "$MOUSE_X"
tmux set-option -g @tabby_last_click_y "$MOUSE_Y"
tmux set-option -g @tabby_last_click_pane "$PANE_ID"

# Select the header pane - this triggers FocusMsg in its BubbleTea
# The header's handleFocusGain() reads the stored click position and processes it
tmux select-pane -t "$PANE_ID"
SCRIPT_EOF
chmod +x "$CLICK_HANDLER_SCRIPT"

# Bind MouseDown1Pane:
# 1. Check target pane command
# 2. If Sidebar or Header: send-keys -M (Pass mouse ONLY. Do not select-pane, let app handle it)
# 3. If Normal: select-pane (Instant focus) AND signal daemon immediately
tmux bind-key -T root MouseDown1Pane \
    if-shell -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" \
        "send-keys -M" \
        "if-shell -F -t = \"#{m:*pane-header*,#{pane_current_command}}\" \
            \"send-keys -M\" \
            \"select-pane -t = ; run-shell -b 'kill -USR1 \$(cat /tmp/tabby-daemon-#{session_id}.pid 2>/dev/null) 2>/dev/null || true'\""

# Enable focus events
tmux set-option -g focus-events on

# Create wrapper script for kill-pane that saves layout first
KILL_PANE_SCRIPT="$CURRENT_DIR/scripts/kill_pane_wrapper.sh"
cat > "$KILL_PANE_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Save layout before killing pane to preserve ratios
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"$CURRENT_DIR/scripts/save_pane_layout.sh"
sleep 0.01
tmux kill-pane "$@"
SCRIPT_EOF
chmod +x "$KILL_PANE_SCRIPT"

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
    "Kill Pane" "x" "run-shell '$KILL_PANE_SCRIPT'"


# Terminal title configuration
# Read from config.yaml or use defaults
TITLE_ENABLED=$(grep -A2 "^terminal_title:" "$CONFIG_FILE" 2>/dev/null | grep "enabled:" | awk '{print $2}' || echo "true")
TITLE_FORMAT=$(grep -A2 "^terminal_title:" "$CONFIG_FILE" 2>/dev/null | grep "format:" | sed 's/.*format: *"\([^"]*\)".*/\1/' || echo "tmux #{window_index}.#{pane_index} #{window_name} #{pane_current_command}")

if [[ "$TITLE_ENABLED" != "false" ]]; then
    tmux set-option -g set-titles on
    tmux set-option -g set-titles-string "$TITLE_FORMAT"
fi

# Read configuration
POSITION=$(grep "^position:" "$CONFIG_FILE" 2>/dev/null | awk '{print $2}' || echo "top")
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
    tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%' ; set-window-option @tabby_name_locked 1"
    tmux bind-key -T root MouseDown1StatusRight new-window
fi

# Helper script to signal sidebar refresh
# Sends SIGUSR1 to sidebar process via PID file
SIGNAL_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/signal_sidebar.sh"

# Create the signal helper script
cat > "$SIGNAL_SIDEBAR_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Signal daemon to refresh window list (instant re-render + spawn new renderers)
SESSION_ID=$(tmux display-message -p '#{session_id}')
PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"

if [ -f "$PID_FILE" ]; then
    kill -USR1 "$(cat "$PID_FILE")" 2>/dev/null || true
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

# Create script to apply saved group to new window
APPLY_GROUP_SCRIPT="$CURRENT_DIR/scripts/apply_new_window_group.sh"
cat > "$APPLY_GROUP_SCRIPT" << 'SCRIPT_EOF'
#!/usr/bin/env bash
# Apply saved group to newly created window
SAVED_GROUP=$(tmux show-option -gqv @tabby_new_window_group)
if [ -n "$SAVED_GROUP" ]; then
    tmux set-window-option @tabby_group "$SAVED_GROUP"
    # Clear the saved group
    tmux set-option -gu @tabby_new_window_group
fi
SCRIPT_EOF
chmod +x "$APPLY_GROUP_SCRIPT"

# Window history tracking for proper focus restoration on window close
TRACK_WINDOW_HISTORY_SCRIPT="$CURRENT_DIR/scripts/track_window_history.sh"
chmod +x "$TRACK_WINDOW_HISTORY_SCRIPT"
SELECT_PREVIOUS_WINDOW_SCRIPT="$CURRENT_DIR/scripts/select_previous_window.sh"
chmod +x "$SELECT_PREVIOUS_WINDOW_SCRIPT"
# Define resize script early so it can be used in window-unlinked hook
RESIZE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/resize_sidebar.sh"
chmod +x "$RESIZE_SIDEBAR_SCRIPT"
# Define save layout script early so it can be used in pane hooks
SAVE_LAYOUT_SCRIPT="$CURRENT_DIR/scripts/save_pane_layout.sh"
chmod +x "$SAVE_LAYOUT_SCRIPT"

# Set up hooks for window events
# These hooks trigger both sidebar and status bar refresh
tmux set-hook -g window-linked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
# On window close: select previous from history, preserve sidebar width, then refresh
tmux set-hook -g window-unlinked "run-shell '$SELECT_PREVIOUS_WINDOW_SCRIPT'; run-shell '$RESIZE_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'"
tmux set-hook -g after-new-window "run-shell '$APPLY_GROUP_SCRIPT'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'"
# Combined script to reduce latency + track window history
ON_WINDOW_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_window_select.sh"
chmod +x "$ON_WINDOW_SELECT_SCRIPT"
tmux set-hook -g after-select-window "run-shell '$ON_WINDOW_SELECT_SCRIPT'; run-shell '$TRACK_WINDOW_HISTORY_SCRIPT'"
# Lock window name on manual rename via prefix+, keybinding
# NOTE: We intentionally do NOT use after-rename-window hook because the daemon's
# own rename-window calls would trigger it, locking the daemon out of future updates.
# Instead, we set @tabby_name_locked directly in each user-facing rename path.
tmux bind-key , command-prompt -I "#W" "rename-window '%%' ; set-window-option @tabby_name_locked 1"

# Refresh sidebar and pane bar when pane focus changes
ON_PANE_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_pane_select.sh"
chmod +x "$ON_PANE_SELECT_SCRIPT"
# Use -b flag to run scripts in background so focus happens immediately
# Combined into a single run-shell to reduce process overhead
# optimization: pass args to avoid internal tmux calls
tmux set-hook -g after-select-pane "run-shell -b '$ON_PANE_SELECT_SCRIPT \"#{session_id}\"; $SAVE_LAYOUT_SCRIPT \"#{window_id}\" \"#{window_layout}\"'"
# pane-focus-in is redundant/unreliable, using after-select-pane is sufficient
# tmux set-hook -g pane-focus-in "run-shell '$ON_PANE_SELECT_SCRIPT'; run-shell '$SAVE_LAYOUT_SCRIPT'"
# Update pane bar when panes are split, and preserve group prefixes
PRESERVE_NAME_SCRIPT="$CURRENT_DIR/scripts/preserve_window_name.sh"
chmod +x "$PRESERVE_NAME_SCRIPT"
tmux set-hook -g after-split-window "run-shell '$PRESERVE_NAME_SCRIPT'; run-shell '$SAVE_LAYOUT_SCRIPT'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_PANE_BAR_SCRIPT'"

# Close window if only sidebar/tabbar remains after main pane exits
CLEANUP_SCRIPT="$CURRENT_DIR/scripts/cleanup_orphan_sidebar.sh"
chmod +x "$CLEANUP_SCRIPT"
PRESERVE_RATIOS_SCRIPT="$CURRENT_DIR/scripts/preserve_pane_ratios.sh"
chmod +x "$PRESERVE_RATIOS_SCRIPT"
# When a pane exits: preserve ratios, cleanup orphans, refresh sidebar and pane bar
tmux set-hook -g pane-exited "run-shell '$PRESERVE_RATIOS_SCRIPT'; run-shell '$CLEANUP_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$SIGNAL_PANE_BAR_SCRIPT'"

# Restore sidebar when client reattaches to session
tmux set-hook -g client-attached "run-shell '$RESTORE_SIDEBAR_SCRIPT'"

# Ensure sidebar/tabbar panes exist for newly created sessions/windows
# (do not toggle global mode on session creation)
tmux set-hook -g session-created "run-shell '$ENSURE_SIDEBAR_SCRIPT'"

# Maintain sidebar width after terminal resize
# (RESIZE_SIDEBAR_SCRIPT already defined above for window-unlinked hook)
tmux set-hook -g client-resized "run-shell '$RESIZE_SIDEBAR_SCRIPT'"

# Override tmux's native window/session chooser to prevent conflicts with sidebar
# prefix + w normally shows choose-tree -Zw (window list)
# prefix + s normally shows choose-tree -Zs (session list)
# These conflict with tabby's sidebar, so we unbind them
tmux unbind-key -T prefix w 2>/dev/null || true
tmux unbind-key -T prefix s 2>/dev/null || true

# Configure sidebar toggle keybinding
TOGGLE_KEY=$(grep "toggle_sidebar:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "prefix + Tab")
KEY=${TOGGLE_KEY##*+ }
if [ -z "$KEY" ]; then KEY="Tab"; fi

tmux bind-key "$KEY" run-shell "$CURRENT_DIR/scripts/toggle_sidebar.sh"

# Optional: Also bind to a prefix-less key for quick access
# tmux bind-key -n M-Tab run-shell "$CURRENT_DIR/scripts/toggle_sidebar.sh"

# Mode toggle and switching
tmux bind-key M run-shell "$CURRENT_DIR/scripts/toggle_mode.sh"
tmux bind-key V run-shell "$CURRENT_DIR/scripts/switch_to_vertical.sh"
tmux bind-key H run-shell "$CURRENT_DIR/scripts/switch_to_horizontal.sh"

# New Group shortcut (prefix + G)
tmux bind-key G command-prompt -p 'New group name:' "run-shell '$CURRENT_DIR/scripts/new_group.sh %%'"

# Override kill-pane to save layout first (preserves pane ratios)
tmux bind-key x run-shell "$KILL_PANE_SCRIPT"

# Keyboard shortcuts follow tmux conventions (prefix-based)
# Standard tmux bindings preserved:
#   prefix + c = new window (enhanced with group capture)
#   prefix + n = next window (tmux default)
#   prefix + p = previous window (tmux default)
#   prefix + " = split horizontal (enhanced with current path)
#   prefix + % = split vertical (enhanced with current path)
#   prefix + x = kill pane (enhanced with ratio preservation)
#   prefix + d = detach (tmux default)
#   prefix + w = window list (tmux default - NOT overridden)
#   prefix + , = rename window (enhanced with name locking)
#   prefix + q = display panes (tmux default)

# Direct window access with prefix + number (standard tmux)
tmux bind-key 1 select-window -t :=0
tmux bind-key 2 select-window -t :=1
tmux bind-key 3 select-window -t :=2
tmux bind-key 4 select-window -t :=3
tmux bind-key 5 select-window -t :=4
tmux bind-key 6 select-window -t :=5
tmux bind-key 7 select-window -t :=6
tmux bind-key 8 select-window -t :=7
tmux bind-key 9 select-window -t :=8
tmux bind-key 0 select-window -t :=9

# First-run bootstrap: if no mode has ever been set, default to enabled.
INITIAL_MODE=$(tmux show-options -gqv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$INITIAL_MODE" ]; then
    tmux set-option -g @tmux-tabs-sidebar "enabled"
    tmux run-shell -b "$ENSURE_SIDEBAR_SCRIPT"
fi
