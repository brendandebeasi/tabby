#!/usr/bin/env bash
# Tabby plugin entry point
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
TABBY_BASE_INDEX=$(tmux show-option -gqv "@tabby_base_index")
if [ "$TABBY_BASE_INDEX" = "1" ] || [ "$TABBY_BASE_INDEX" = "true" ]; then
    tmux set-option -g base-index 1
    tmux set-window-option -g pane-base-index 1
fi

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
tmux bind-key '|' split-window -h -c "#{pane_current_path}"
tmux bind-key '-' split-window -v -c "#{pane_current_path}"

# Create script to capture current window's group, create new window,
# spawn sidebar, and restore focus — all synchronously in one shot.
NEW_WINDOW_SCRIPT="$CURRENT_DIR/scripts/new_window_with_group.sh"
cat > "$NEW_WINDOW_SCRIPT" << SCRIPT_EOF
#!/usr/bin/env bash
set -eu

PLUGIN_DIR="$CURRENT_DIR"
RENDERER_BIN="\$PLUGIN_DIR/bin/sidebar-renderer"

tmux set-option -g @tabby_spawning 1 2>/dev/null || true
# NOTE: Do NOT clear @tabby_spawning in an EXIT trap -- the after-new-window
# hook fires AFTER run-shell completes, so an EXIT trap would clear the
# guard before hooks can check it.  Clear it asynchronously with a delay
# so hooks that check @tabby_spawning (ensure_sidebar, enforce_status)
# still see it as set.
cleanup_spawning() { sleep 0.3; tmux set-option -gu @tabby_spawning 2>/dev/null || true; }
trap 'cleanup_spawning &' EXIT

SAVED_GROUP=\$(tmux show-option -gqv @tabby_new_window_group 2>/dev/null || echo "")
SAVED_PATH=\$(tmux show-option -gqv @tabby_new_window_path 2>/dev/null || echo "")

if [ -z "\$SAVED_GROUP" ]; then
    SAVED_GROUP=\$(tmux show-window-options -v @tabby_group 2>/dev/null || echo "")
fi

if [ -z "\$SAVED_PATH" ]; then
    SAVED_PATH=\$(tmux display-message -p "#{pane_current_path}")
fi

# Capture the current window's prompt icon and pane colors so we can pre-set
# them on the new window BEFORE the shell starts (the daemon's USR1 refresh
# is async and would arrive too late for the initial prompt render).
SAVED_ICON=\$(tmux show-option -wqv @tabby_prompt_icon 2>/dev/null || echo "")
SAVED_PANE_ACTIVE=\$(tmux show-option -wqv @tabby_pane_active 2>/dev/null || echo "")
SAVED_PANE_INACTIVE=\$(tmux show-option -wqv @tabby_pane_inactive 2>/dev/null || echo "")
NEW_WINDOW_ID=\$(tmux new-window -P -F "#{window_id}" -c "\$SAVED_PATH" 2>/dev/null || true)
NEW_WINDOW_ID=\$(printf "%s" "\$NEW_WINDOW_ID" | tr -d '\r\n')

if [ -n "\$NEW_WINDOW_ID" ] && [ -n "\$SAVED_GROUP" ] && [ "\$SAVED_GROUP" != "Default" ]; then
    tmux set-window-option -t "\$NEW_WINDOW_ID" @tabby_group "\$SAVED_GROUP" 2>/dev/null || true
fi

# Pre-set prompt icon and pane colors on the new window so the shell sees
# them immediately (same group = same icon/colors; daemon will correct later
# if they differ).
if [ -n "\$NEW_WINDOW_ID" ] && [ -n "\$SAVED_ICON" ]; then
    tmux set-window-option -t "\$NEW_WINDOW_ID" @tabby_prompt_icon "\$SAVED_ICON" 2>/dev/null || true
fi
if [ -n "\$NEW_WINDOW_ID" ] && [ -n "\$SAVED_PANE_ACTIVE" ]; then
    tmux set-window-option -t "\$NEW_WINDOW_ID" @tabby_pane_active "\$SAVED_PANE_ACTIVE" 2>/dev/null || true
fi
if [ -n "\$NEW_WINDOW_ID" ] && [ -n "\$SAVED_PANE_INACTIVE" ]; then
    tmux set-window-option -t "\$NEW_WINDOW_ID" @tabby_pane_inactive "\$SAVED_PANE_INACTIVE" 2>/dev/null || true
fi
# Spawn sidebar renderer directly so the window is fully set up before
# any hooks fire.  The daemon will see the renderer already exists and skip.
MODE=\$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
if [ "\$MODE" = "enabled" ] && [ -n "\$NEW_WINDOW_ID" ] && [ -x "\$RENDERER_BIN" ]; then
    SESSION_ID=\$(tmux display-message -p '#{session_id}')
    SIDEBAR_WIDTH=\$(tmux show-option -gqv @tabby_sidebar_width 2>/dev/null || echo "25")
    [ -z "\$SIDEBAR_WIDTH" ] && SIDEBAR_WIDTH=25
    FIRST_PANE=\$(tmux list-panes -t "\$NEW_WINDOW_ID" -F "#{pane_id}" 2>/dev/null | head -1)
    if [ -n "\$FIRST_PANE" ]; then
        DEBUG_FLAG=""
        [ "\${TABBY_DEBUG:-}" = "1" ] && DEBUG_FLAG="-debug"
        tmux split-window -d -t "\$FIRST_PANE" -h -b -f -l "\$SIDEBAR_WIDTH" \\
            "exec '\$RENDERER_BIN' -session '\$SESSION_ID' -window '\$NEW_WINDOW_ID' \$DEBUG_FLAG" 2>/dev/null || true
    fi
fi

# Ensure the new window has focus (tmux new-window usually does this,
# but sidebar spawn + hooks can steal it)
if [ -n "\$NEW_WINDOW_ID" ]; then
    tmux select-window -t "\$NEW_WINDOW_ID" 2>/dev/null || true
    # Select the content pane (not the sidebar) — sidebar is spawned with -b
    # so it is pane index 0; the content pane is the last pane in the window.
    CONTENT_PANE=\$(tmux list-panes -t "\$NEW_WINDOW_ID" -F '#{pane_id}' 2>/dev/null | tail -1)
    [ -n "\$CONTENT_PANE" ] && tmux select-pane -t "\$CONTENT_PANE" 2>/dev/null || true
fi

tmux set-option -gu @tabby_new_window_group 2>/dev/null || true
tmux set-option -gu @tabby_new_window_path 2>/dev/null || true

# Single signal to daemon for rendering update
PID_FILE="/tmp/tabby-daemon-\$(tmux display-message -p '#{session_id}').pid"
[ -f "\$PID_FILE" ] && kill -USR1 "\$(cat "\$PID_FILE")" 2>/dev/null || true
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

# Inactive pane dimming: handled by bin/cycle-pane binary (--dim-only flag)
# Applied on plugin load and after pane cycling. Skips utility panes.
CYCLE_PANE_BIN="$CURRENT_DIR/bin/cycle-pane"
tmux set-option -g window-style "default"
tmux set-option -g window-active-style "default"
if [ -x "$CYCLE_PANE_BIN" ]; then
    "$CYCLE_PANE_BIN" --dim-only
fi

# Keep session windows sized to the most recently active client so mobile
# attaches do not end up with off-screen sidebars in dual-client setups.
tmux set-window-option -g window-size "latest"

# Pane header format: hide for utility panes (sidebar, pane-bar, tabbar)

# Unbind right-click on pane so it passes through to apps with mouse capture
# (sidebar-renderer / pane-header use BubbleTea mouse mode and handle right-click internally)
tmux unbind-key -T root MouseDown3Pane 2>/dev/null || true
tmux bind-key -T root MouseDown3Pane send-keys -M -t =

# Keep utility-pane drag events forwarded, but force tmux copy-drag in normal panes.
tmux unbind-key -T root MouseDrag1Pane 2>/dev/null || true
tmux bind-key -T root MouseDrag1Pane \
    if-shell -F -t = "#{||:#{m:*sidebar-render*,#{pane_current_command}},#{m:*pane-header*,#{pane_current_command}}}" \
        "send-keys -M -t =" \
        "select-pane -t = ; copy-mode -M"

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

tmux select-pane -t "$PANE_ID"
SCRIPT_EOF
chmod +x "$CLICK_HANDLER_SCRIPT"

# Bind MouseDown1Pane:
# 1. Check target pane command
# 2. If Sidebar or Header: send-keys -M (Pass mouse ONLY. Do not select-pane, let app handle it)
# 3. If Normal: select-pane (Instant focus) AND signal daemon immediately
tmux bind-key -T root MouseDown1Pane \
    if-shell -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" \
        "send-keys -M -t =" \
        "if-shell -F -t = \"#{m:*pane-header*,#{pane_current_command}}\" \
		    \"run-shell -b '$CLICK_HANDLER_SCRIPT \\\"#{pane_id}\\\" \\\"#{mouse_x}\\\" \\\"#{mouse_y}\\\"'\" \
            \"select-pane -t = ; send-keys -M -t = ; run-shell -b 'kill -USR1 \$(cat /tmp/tabby-daemon-#{session_id}.pid 2>/dev/null) 2>/dev/null || true'\""

tmux bind-key -T root MouseUp1Pane \
    if-shell -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" \
        "send-keys -M -t =" \
        "if-shell -F -t = \"#{m:*pane-header*,#{pane_current_command}}\" \
		    \"\" \
            \"select-pane -t = ; send-keys -M -t = ; run-shell -b 'kill -USR1 \$(cat /tmp/tabby-daemon-#{session_id}.pid 2>/dev/null) 2>/dev/null || true'\""

tmux unbind-key -T root MouseUp3Pane 2>/dev/null || true
tmux bind-key -T root MouseUp3Pane send-keys -M -t =

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

# Pane border mouse: left-click+drag resizes panes, right-click shows context menu
# IMPORTANT: MouseDown1Border must stay bound for MouseDrag1Border (resize) to work
tmux bind-key -T root MouseDown1Border select-pane -t =
tmux bind-key -T root MouseDrag1Border resize-pane -M
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

# Sidebar width safety caps for mobile/focus view
SIDEBAR_MOBILE_MAX_PERCENT=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "mobile_max_percent:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_MOBILE_MIN_CONTENT=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "mobile_min_content_cols:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_MOBILE_MAX_WINDOW=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "mobile_max_window_cols:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_TABLET_MAX_WINDOW=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "tablet_max_window_cols:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_WIDTH_MOBILE=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "width_mobile:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_WIDTH_TABLET=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "width_tablet:" | awk '{print $2}' | tr -d '"' || echo "")
SIDEBAR_WIDTH_DESKTOP=$(grep -A40 "^sidebar:" "$CONFIG_FILE" 2>/dev/null | grep "width_desktop:" | awk '{print $2}' | tr -d '"' || echo "")

SIDEBAR_MOBILE_MAX_PERCENT=${SIDEBAR_MOBILE_MAX_PERCENT:-20}
SIDEBAR_MOBILE_MIN_CONTENT=${SIDEBAR_MOBILE_MIN_CONTENT:-40}
SIDEBAR_MOBILE_MAX_WINDOW=${SIDEBAR_MOBILE_MAX_WINDOW:-110}
SIDEBAR_TABLET_MAX_WINDOW=${SIDEBAR_TABLET_MAX_WINDOW:-170}
SIDEBAR_WIDTH_MOBILE=${SIDEBAR_WIDTH_MOBILE:-15}
SIDEBAR_WIDTH_TABLET=${SIDEBAR_WIDTH_TABLET:-20}
SIDEBAR_WIDTH_DESKTOP=${SIDEBAR_WIDTH_DESKTOP:-25}

tmux set-option -g @tabby_sidebar_mobile_max_percent "$SIDEBAR_MOBILE_MAX_PERCENT"
tmux set-option -g @tabby_sidebar_mobile_min_content_cols "$SIDEBAR_MOBILE_MIN_CONTENT"
tmux set-option -g @tabby_sidebar_mobile_max_window_cols "$SIDEBAR_MOBILE_MAX_WINDOW"
tmux set-option -g @tabby_sidebar_tablet_max_window_cols "$SIDEBAR_TABLET_MAX_WINDOW"
tmux set-option -g @tabby_sidebar_width_mobile "$SIDEBAR_WIDTH_MOBILE"
tmux set-option -g @tabby_sidebar_width_tablet "$SIDEBAR_WIDTH_TABLET"
tmux set-option -g @tabby_sidebar_width_desktop "$SIDEBAR_WIDTH_DESKTOP"

# First-run bootstrap: if no mode has ever been set, default to enabled.
INITIAL_MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")
if [ -z "$INITIAL_MODE" ]; then
    tmux set-option -g @tabby_sidebar "enabled"
fi

# Configure horizontal status bar
if [[ "$POSITION" == "top" ]] || [[ "$POSITION" == "bottom" ]]; then
    # Clear any existing status-format settings that would override window-status
    for i in {0..10}; do
        tmux set-option -gu status-format[$i] 2>/dev/null || true
    done
    tmux set-option -gu status-format 2>/dev/null || true

    # Only enable tmux status bar in disabled mode.
    # In enabled/horizontal modes, Tabby owns the UI surface.
    SIDEBAR_STATE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")
    if [ "$SIDEBAR_STATE" = "disabled" ]; then
        tmux set-option -g status on
    else
        tmux set-option -g status off
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
SESSION_ID="${1:-$(tmux display-message -p '#{session_id}')}"
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

STABILIZE_CLIENT_RESIZE_SCRIPT="$CURRENT_DIR/scripts/stabilize_client_resize.sh"
chmod +x "$STABILIZE_CLIENT_RESIZE_SCRIPT"

# Helper script to enforce status/tabby mutual exclusivity
STATUS_GUARD_SCRIPT="$CURRENT_DIR/scripts/enforce_status_exclusivity.sh"
chmod +x "$STATUS_GUARD_SCRIPT"

FOCUS_RECOVERY_SCRIPT="$CURRENT_DIR/scripts/restore_input_focus.sh"
chmod +x "$FOCUS_RECOVERY_SCRIPT"

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
EXIT_IF_NO_MAIN_WINDOWS_SCRIPT="$CURRENT_DIR/scripts/exit_if_no_main_windows.sh"
chmod +x "$EXIT_IF_NO_MAIN_WINDOWS_SCRIPT"
# Define resize script early so it can be used in window-unlinked hook
RESIZE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/resize_sidebar.sh"
chmod +x "$RESIZE_SIDEBAR_SCRIPT"
# Define save layout script early so it can be used in pane hooks
SAVE_LAYOUT_SCRIPT="$CURRENT_DIR/scripts/save_pane_layout.sh"
chmod +x "$SAVE_LAYOUT_SCRIPT"

# Set up hooks for window events
# These hooks trigger both sidebar and status bar refresh
tmux set-hook -g window-linked "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"
# On window close: select previous window (sync for UX), then background the rest.
# The daemon handles orphan cleanup, sidebar spawning, and pane-bar signaling on USR1.
tmux set-hook -g window-unlinked "run-shell '$SELECT_PREVIOUS_WINDOW_SCRIPT'; run-shell -b '$RESIZE_SIDEBAR_SCRIPT; $SIGNAL_SIDEBAR_SCRIPT; $REFRESH_STATUS_SCRIPT; $EXIT_IF_NO_MAIN_WINDOWS_SCRIPT; $STATUS_GUARD_SCRIPT \"#{session_id}\"'"
# Note: after-kill-window is not a valid hook in tmux 3.6+; window-unlinked
# covers the window-close case. exit_if_no_main runs in the background chains above.
# after-new-window: apply group label, then let ensure_sidebar handle the
# single USR1 signal to the daemon (which spawns the renderer).  We do NOT
# also call signal_sidebar here — that would send a duplicate USR1 before
# ensure_sidebar's own signal, causing the sidebar to "juggle" visually.
tmux set-hook -g after-new-window "run-shell '$APPLY_GROUP_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"
# Combined script to reduce latency + track window history
ON_WINDOW_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_window_select.sh"
chmod +x "$ON_WINDOW_SELECT_SCRIPT"
tmux set-hook -g after-select-window "run-shell '$ON_WINDOW_SELECT_SCRIPT'; run-shell '$TRACK_WINDOW_HISTORY_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'; run-shell -b '[ -x \"$CYCLE_PANE_BIN\" ] && \"$CYCLE_PANE_BIN\" --dim-only'"
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
tmux set-hook -g after-select-pane "run-shell -b '$ON_PANE_SELECT_SCRIPT \"#{session_id}\"; $SAVE_LAYOUT_SCRIPT \"#{window_id}\" \"#{window_layout}\"; [ -x \"$CYCLE_PANE_BIN\" ] && \"$CYCLE_PANE_BIN\" --dim-only'"
# pane-focus-in is redundant/unreliable, using after-select-pane is sufficient
# tmux set-hook -g pane-focus-in "run-shell '$ON_PANE_SELECT_SCRIPT'; run-shell '$SAVE_LAYOUT_SCRIPT'"
# Update pane bar when panes are split, and preserve group prefixes
PRESERVE_NAME_SCRIPT="$CURRENT_DIR/scripts/preserve_window_name.sh"
chmod +x "$PRESERVE_NAME_SCRIPT"
tmux set-hook -g after-split-window "run-shell -b '$SIGNAL_SIDEBAR_SCRIPT #{session_id}'; run-shell '$PRESERVE_NAME_SCRIPT'; run-shell '$SAVE_LAYOUT_SCRIPT #{window_id} #{window_layout}'; run-shell '$SIGNAL_PANE_BAR_SCRIPT'"

# When a pane is killed: preserve ratios synchronously (must happen before tmux
# reflows), then signal daemon in background. The daemon's USR1 handler takes
# care of orphan cleanup, sidebar spawning, and pane-bar refresh.
# Note: uses after-kill-pane (not pane-exited which doesn't exist in tmux 3.6+).
PRESERVE_RATIOS_SCRIPT="$CURRENT_DIR/scripts/preserve_pane_ratios.sh"
chmod +x "$PRESERVE_RATIOS_SCRIPT"
tmux set-hook -g after-kill-pane "run-shell '$PRESERVE_RATIOS_SCRIPT'; run-shell -b '$SIGNAL_SIDEBAR_SCRIPT; $EXIT_IF_NO_MAIN_WINDOWS_SCRIPT; $STATUS_GUARD_SCRIPT \"#{session_id}\"'"

# Restore sidebar when client reattaches to session
tmux set-hook -g client-attached "run-shell '$RESTORE_SIDEBAR_SCRIPT'; run-shell '$STABILIZE_CLIENT_RESIZE_SCRIPT \"#{session_id}\" \"#{window_id}\" \"#{client_tty}\" \"#{client_width}\" \"#{client_height}\"'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"

# Ensure sidebar/tabbar panes exist for newly created sessions/windows
# (do not toggle global mode on session creation)
tmux set-hook -g session-created "run-shell '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"

# Maintain sidebar width after terminal resize
# (RESIZE_SIDEBAR_SCRIPT already defined above for window-unlinked hook)
tmux set-hook -g client-resized "run-shell '$RESIZE_SIDEBAR_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"

# Keep tmux native chooser shortcuts available
tmux bind-key w choose-tree -Zw
tmux bind-key s choose-tree -Zs

# Configure sidebar toggle keybinding
TOGGLE_KEY=$(grep "toggle_sidebar:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "prefix + Tab")
KEY=${TOGGLE_KEY##*+ }
if [ -z "$KEY" ]; then KEY="Tab"; fi

tmux bind-key "$KEY" run-shell "$CURRENT_DIR/scripts/toggle_sidebar.sh"

normalize_global_key() {
	local key="$1"
	if [[ "$key" == cmd+shift+[ ]]; then
		echo "M-{"
		return
	fi
	if [[ "$key" == cmd+shift+] ]]; then
		echo "M-}"
		return
	fi
	if [[ "$key" == cmd+shift+* ]]; then
		echo "M-S-${key#cmd+shift+}"
		return
	fi
	if [[ "$key" == cmd+* ]]; then
		echo "M-${key#cmd+}"
		return
	fi
	echo "$key"
}

bind_from_config() {
	local binding="$1"
	local command="$2"
	local key
	if [ -z "$binding" ]; then
		return
	fi
	if [[ "$binding" == prefix* ]]; then
		key=${binding##*+ }
		[ -n "$key" ] && tmux bind-key "$key" "$command"
		return
	fi
	key=$(normalize_global_key "$binding")
	[ -n "$key" ] && tmux bind-key -n "$key" "$command"
}

NEXT_WINDOW_BINDING=$(grep "next_window_global:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
PREV_WINDOW_BINDING=$(grep "prev_window_global:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
if [ -z "$NEXT_WINDOW_BINDING" ]; then
	NEXT_WINDOW_BINDING=$(grep "next_window:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
fi
if [ -z "$PREV_WINDOW_BINDING" ]; then
	PREV_WINDOW_BINDING=$(grep "prev_window:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
fi

NEW_WINDOW_BINDING=$(grep "new_window_global:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
KILL_WINDOW_BINDING=$(grep "kill_window_global:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")

bind_from_config "$NEXT_WINDOW_BINDING" "next-window"
bind_from_config "$PREV_WINDOW_BINDING" "previous-window"
bind_from_config "$NEW_WINDOW_BINDING" "run-shell '$NEW_WINDOW_SCRIPT'"
bind_from_config "$KILL_WINDOW_BINDING" "kill-window"

# Swap/cycle active pane within current window (skips utility panes, signals daemon)
# Uses Go binary: bin/cycle-pane (also handles dimming)
SWAP_PANE_BINDING=$(grep "swap_pane:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
if [ -n "$SWAP_PANE_BINDING" ] && [ -x "$CYCLE_PANE_BIN" ]; then
    SWAP_KEY=$(normalize_global_key "$SWAP_PANE_BINDING")
    [ -n "$SWAP_KEY" ] && tmux bind-key -n "$SWAP_KEY" run-shell "$CYCLE_PANE_BIN"
fi
# Also override prefix+o to use the smart cycle binary
if [ -x "$CYCLE_PANE_BIN" ]; then
    tmux bind-key o run-shell "$CYCLE_PANE_BIN"
fi

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
#   prefix + w = window list (choose-tree)
#   prefix + s = session list (choose-tree)
#   prefix + , = rename window (enhanced with name locking)
#   prefix + q = display panes (tmux default)

# Direct window access with prefix + number (match tmux window indexes)
tmux bind-key 0 select-window -t :0
tmux bind-key 1 select-window -t :1
tmux bind-key 2 select-window -t :2
tmux bind-key 3 select-window -t :3
tmux bind-key 4 select-window -t :4
tmux bind-key 5 select-window -t :5
tmux bind-key 6 select-window -t :6
tmux bind-key 7 select-window -t :7
tmux bind-key 8 select-window -t :8
tmux bind-key 9 select-window -t :9

# Legacy Alt-key shortcuts kept for fast navigation
tmux bind-key -n M-h previous-window
tmux bind-key -n M-l next-window
tmux bind-key -n M-n run-shell "$NEW_WINDOW_SCRIPT"
tmux bind-key -n M-N run-shell "$NEW_WINDOW_SCRIPT"
tmux bind-key -n M-x run-shell "$KILL_PANE_SCRIPT"
tmux bind-key -n M-q display-panes
tmux bind-key -n M-0 select-window -t :0
tmux bind-key -n M-1 select-window -t :1
tmux bind-key -n M-2 select-window -t :2
tmux bind-key -n M-3 select-window -t :3
tmux bind-key -n M-4 select-window -t :4
tmux bind-key -n M-5 select-window -t :5
tmux bind-key -n M-6 select-window -t :6
tmux bind-key -n M-7 select-window -t :7
tmux bind-key -n M-8 select-window -t :8
tmux bind-key -n M-9 select-window -t :9

# Some terminals send Alt/Meta + Shift + digit as punctuation (e.g. !, @, #, $).
# Bind those too so Cmd+Shift+N -> Meta+<punct> mappings still switch windows.
tmux bind-key -n M-! select-window -t :1
tmux bind-key -n M-@ select-window -t :2
tmux bind-key -n M-# select-window -t :3
tmux bind-key -n M-$ select-window -t :4
tmux bind-key -n M-% select-window -t :5

# Ensure mode surfaces are present on load.
# This covers first-run bootstrap and config reloads where mode is already set
# but daemon/renderers are not running yet.
# Run synchronously (no -b) so the sidebar is ready before the first prompt appears.
# session-created and client-attached hooks fire before tabby.tmux runs, so this
# is the only reliable bootstrap path for the initial session.
_TABBY_BOOT_T0=$(perl -MTime::HiRes=time -e 'printf "%.0f\n", time * 1000' 2>/dev/null || date +%s)
tmux run-shell "$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\""
_TABBY_BOOT_T1=$(perl -MTime::HiRes=time -e 'printf "%.0f\n", time * 1000' 2>/dev/null || date +%s)
printf "%s tabby_bootstrap_ms=%s\n" "$(date '+%Y-%m-%d %H:%M:%S')" "$((_TABBY_BOOT_T1 - _TABBY_BOOT_T0))" >> /tmp/tabby-startup.log 2>/dev/null || true

tmux run-shell -b "$STATUS_GUARD_SCRIPT \"#{session_id}\""
tmux run-shell -b "$FOCUS_RECOVERY_SCRIPT \"#{session_id}\""
