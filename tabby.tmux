#!/usr/bin/env bash
# Tabby plugin entry point
# Fixes: BUG-003 (hook signal targeting)
#
# Two-phase init for instant sidebar:
#   Phase 1 (sync):  pre-start daemon + split sidebar pane (~20ms)
#   Phase 2 (async): hooks, bindings, options via TABBY_DEFERRED=1 re-entry

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Optional kill-switch for troubleshooting.
TABBY_ENABLED=$(tmux show-option -gqv "@tabby_enabled")
if [ "$TABBY_ENABLED" = "0" ]; then
    exit 0
fi

# Build binaries if not present
if [ ! -f "$CURRENT_DIR/bin/render-status" ]; then
    "$CURRENT_DIR/scripts/install.sh" || true
fi

# --- Phase 1: Fast path (daemon pre-start + instant sidebar pane) ---
# Minimize tmux round-trips for speed. Batch queries where possible.
# The daemon's width sync will correct the sidebar width on connect.
if [ "${TABBY_DEFERRED:-}" != "1" ]; then
    # Single tmux call for session + window + sidebar mode (3 values, 1 round-trip)
    _TMUX_INFO=$(tmux display-message -p '#{session_id}|#{window_id}' 2>/dev/null || echo "|")
    _SESSION="${_TMUX_INFO%%|*}"
    _WINDOW="${_TMUX_INFO#*|}"
    _MODE=$(tmux show-options -gqv @tabby_sidebar 2>/dev/null || echo "")

    # Default mode is "enabled", so act when enabled OR not yet set
    if [ -n "$_SESSION" ] && { [ "$_MODE" = "enabled" ] || [ -z "$_MODE" ]; }; then
        # Pre-start daemon if not already running
        _SOCK="/tmp/tabby-daemon-${_SESSION}.sock"
        _PIDF="/tmp/tabby-daemon-${_SESSION}.pid"
        _WDF="/tmp/tabby-daemon-${_SESSION}.watchdog.pid"
        _ALIVE=false
        if [ -S "$_SOCK" ] && [ -f "$_PIDF" ]; then
            _PID=$(cat "$_PIDF" 2>/dev/null || echo "")
            [ -n "$_PID" ] && kill -0 "$_PID" 2>/dev/null && _ALIVE=true
        fi
        if [ "$_ALIVE" = "false" ]; then
            _WD_ALIVE=false
            if [ -f "$_WDF" ]; then
                _WP=$(cat "$_WDF" 2>/dev/null || echo "")
                [ -n "$_WP" ] && kill -0 "$_WP" 2>/dev/null && _WD_ALIVE=true
            fi
            if [ "$_WD_ALIVE" = "false" ]; then
                rm -f "$_SOCK" "$_PIDF"
                "$CURRENT_DIR/bin/tabby" watchdog -session "$_SESSION" &
            fi
        fi

        # Instant sidebar pane — single list-panes call for both check and pane ID
        # Renderers are now subcommands of tabby; exec -a preserves the legacy
        # argv[0] so pane_current_command still reports "sidebar-renderer" for
        # detection logic in cycle-pane and the daemon.
        _TABBY_BIN="$CURRENT_DIR/bin/tabby"
        if [ -x "$_TABBY_BIN" ]; then
            _PANES=$(tmux list-panes -F "#{pane_id}|#{pane_current_command}|#{pane_start_command}" 2>/dev/null)
            _HAS_SIDEBAR=$(echo "$_PANES" | grep -qE "sidebar" && echo "y" || echo "")
            if [ -z "$_HAS_SIDEBAR" ]; then
                _FIRST_PANE=$(echo "$_PANES" | head -1 | cut -d'|' -f1)
                if [ -n "$_FIRST_PANE" ] && [ -n "$_WINDOW" ]; then
                    _DBG=""; [ "${TABBY_DEBUG:-}" = "1" ] && _DBG="-debug"
                    tmux split-window -d -t "$_FIRST_PANE" -h -b -f -l 25 \
                        "printf '\\033[?25l\\033[2J\\033[H' && exec -a sidebar-renderer '$_TABBY_BIN' render sidebar -session '$_SESSION' -window '$_WINDOW' $_DBG"
                fi
            fi
        fi
    fi

    # Async handoff: defer 106 tmux set-option/bind-key/set-hook calls to background
    tmux run-shell -b "TABBY_DEFERRED=1 '$CURRENT_DIR/tabby.tmux'"
    exit 0
fi

# ============================================================
# Phase 2: Deferred init (hooks, bindings, options)
# Runs async — tmux has already rendered the sidebar pane.
# ============================================================

# Resolve config path (only needed for deferred init, not fast path)
source "$CURRENT_DIR/scripts/_config_path.sh"
CONFIG_FILE="$TABBY_CONFIG_FILE"

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

# Window sizing: resize all windows/panes together when terminal resizes.
# NOTE: `window-size manual` segfaults homebrew tmux inside
# clients_calculate_size during new-session spawn (null deref in
# default_window_size). Do NOT use `manual` here. If flicker mitigation
# in multi-client setups is needed, do it via explicit resize-window
# calls from the daemon instead of via this global option.
tmux set-option -g window-size latest
tmux set-option -g aggressive-resize on

# New panes/windows open in the current content pane's directory.
# If focus is on a window-header utility pane, split the underlying content pane.
SPLIT_PANE_CMD="$CURRENT_DIR/bin/tabby hook split-pane"
tmux bind-key '"' run-shell "$SPLIT_PANE_CMD v"
tmux bind-key '%' run-shell "$SPLIT_PANE_CMD h"
tmux bind-key '|' run-shell "$SPLIT_PANE_CMD h"
tmux bind-key '-' run-shell "$SPLIT_PANE_CMD v"

# Override 'c' to capture group and create new window (Go binary)
NEW_WINDOW_BIN="$CURRENT_DIR/bin/tabby new-window"
tmux bind-key 'c' run-shell "$NEW_WINDOW_BIN -client-tty '#{client_tty}'"

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

# Read terminal_bg for hiding borders (user's terminal background color).
# If unset, keep tmux defaults instead of forcing black.
TERMINAL_BG=$(grep -A20 "^pane_header:" "$CONFIG_FILE" 2>/dev/null | grep "terminal_bg:" | awk '{print $2}' | tr -d '"' || echo "")
TERMINAL_BG=${TERMINAL_BG:-}
tmux set-option -g @tabby_terminal_bg "$TERMINAL_BG"

# Read border line style: single, double, heavy, simple, number
# When custom_border is enabled, force 'simple' (thinnest) to minimize visibility of tmux borders
if [[ "$CUSTOM_BORDER" == "true" ]]; then
    BORDER_LINES="simple"
    # Hide tmux borders by matching terminal background when configured.
    # Otherwise preserve terminal defaults (do not force dark fallback).
    if [ -n "$TERMINAL_BG" ]; then
        tmux set-option -g pane-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
        tmux set-option -g pane-active-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
    else
        tmux set-option -g pane-border-style "fg=default,bg=default"
        tmux set-option -g pane-active-border-style "fg=default,bg=default"
    fi
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
    # Keep separator lines minimal and make borders visually blend into the
    # terminal background so they don't look like a second non-interactive header.
    if [[ "$CUSTOM_BORDER" != "true" ]]; then
        tmux set-option -g pane-border-lines simple
        if [ -n "$TERMINAL_BG" ]; then
            tmux set-option -g pane-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
            tmux set-option -g pane-active-border-style "fg=$TERMINAL_BG,bg=$TERMINAL_BG"
        else
            tmux set-option -g pane-border-style "fg=default,bg=default"
            tmux set-option -g pane-active-border-style "fg=default,bg=default"
        fi
    fi
else
    tmux set-option -g pane-border-status top
    tmux set-option -g pane-border-lines "$BORDER_LINES"
fi

# Pane border styling - colored headers with info
# When custom_border is enabled, borders are hidden (set earlier)
# Otherwise, use the same color for both to prevent half/half on shared edges
if [[ "$CUSTOM_BORDER" != "true" && "$PANE_HEADERS" != "true" ]]; then
    tmux set-option -g pane-border-style "fg=$PANE_ACTIVE_BG"
    tmux set-option -g pane-active-border-style "fg=$PANE_ACTIVE_BG"
fi

# Inactive pane dimming: now handled by tabby-daemon (ApplyPaneDimming on signal_refresh).
# The cycle-pane binary is still used for pane cycling keybinding only.
TABBY_CYCLE_BIN="$CURRENT_DIR/bin/tabby"
CYCLE_PANE_BIN="$CURRENT_DIR/bin/tabby cycle-pane"
# Reset global window styles so the daemon's applyThemeToTmux() takes effect.
# NOTE: We use -ug (unset global) rather than setting to "default" because
# the string "default" resolves to bg=8 (terminal-native), which may not
# match the theme. Unsetting lets tmux use its built-in default until the
# daemon sets the proper themed global style.
tmux set-option -ug window-style
tmux set-option -ug window-active-style

# Pane header format: hide for utility panes (sidebar, window-header)

# Unbind right-click on pane so it passes through to apps with mouse capture
# (sidebar-renderer / window-header use BubbleTea mouse mode and handle right-click internally)
tmux unbind-key -T root MouseDown3Pane 2>/dev/null || true
tmux bind-key -T root MouseDown3Pane send-keys -M -t =

# Keep utility-pane drag events forwarded, but force tmux copy-drag in normal panes.
tmux unbind-key -T root MouseDrag1Pane 2>/dev/null || true
tmux bind-key -T root MouseDrag1Pane \
    if-shell -F -t = "#{||:#{m:*sidebar-render*,#{pane_current_command}},#{||:#{m:*pane-header*,#{pane_current_command}},#{m:*window-header*,#{pane_current_command}}}}" \
        "send-keys -M -t =" \
        "select-pane -t = ; copy-mode -M"

# Handle clicks on window-header panes specially to allow buttons to work regardless of focus
# Architecture: Only intercept window-header clicks. Let sidebar and normal panes use default tmux behavior.
# Flow: MouseDown1Pane -> if window-header: store click pos, select pane -> BubbleTea FocusMsg -> daemon handles action

# Bind MouseDown1Pane:
# 1. Check target pane command
# 2. If Sidebar or Header: send-keys -M (Pass mouse ONLY. Do not select-pane, let app handle it)
# 3. If Normal: select-pane (Instant focus) AND signal daemon immediately
tmux bind-key -T root MouseDown1Pane \
    if-shell -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" \
        "send-keys -M -t =" \
        "if-shell -F -t = \"#{||:#{m:*pane-header*,#{pane_current_command}},#{m:*window-header*,#{pane_current_command}}}\" \
		    \"select-pane -t = ; send-keys -M -t =\" \
            \"select-pane -t = ; send-keys -M -t = ; run-shell -b '$CYCLE_PANE_BIN --dim-only ; $CURRENT_DIR/scripts/signal-daemon.sh'\""

tmux bind-key -T root MouseUp1Pane \
    if-shell -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" \
        "send-keys -M -t =" \
        "if-shell -F -t = \"#{||:#{m:*pane-header*,#{pane_current_command}},#{m:*window-header*,#{pane_current_command}}}\" \
		    \"send-keys -M -t =\" \
            \"select-pane -t = ; send-keys -M -t = ; run-shell -b '$CURRENT_DIR/scripts/signal-daemon.sh'\""

tmux unbind-key -T root MouseUp3Pane 2>/dev/null || true
tmux bind-key -T root MouseUp3Pane send-keys -M -t =

# Scroll wheel: sidebar always gets passthrough via send-keys -M.
# For all other panes:
# - alternate_on/mouse_any_flag/pane_in_mode: fullscreen TUI has mouse — forward the event
# - Otherwise (normal screen): enter tmux copy mode on the pane under the mouse (-t =),
#   which scrolls tmux's captured history. The -e flag auto-exits copy mode when
#   scrolled back to the bottom. This replicates terminal scrollback for SSH panes
#   (e.g. claude code) where the terminal emulator can't scroll its own buffer.
tmux source-file - << 'TMUX_EOF'
bind-key -T root WheelUpPane {
    if -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" {
        send-keys -M -t =
    } {
        if -F -t = "#{mouse_any_flag}" {
            send-keys -M
        } {
            copy-mode -e -t =
        }
    }
}
# WheelDownPane: always forward. Entering copy-mode on scroll-down from the
# bottom (which `copy-mode -e -t =` does when there's nothing below to scroll
# to) makes trackpad inertia pingpong at line 0 in TUIs with mouse reporting
# (Claude Code, nvim with mouse=a). Dropping the alternate_on/pane_in_mode
# disjunction also removes a mid-scroll re-evaluation that could flip the
# branch chosen between consecutive wheel events. See README/scroll notes.
bind-key -T root WheelDownPane {
    if -F -t = "#{m:*sidebar-render*,#{pane_current_command}}" {
        send-keys -M -t =
    } {
        send-keys -M
    }
}
TMUX_EOF

# Enable focus events
tmux set-option -g focus-events on

# Kill-pane: daemon handles save-layout + kill, or kills window if last content pane
KILL_PANE_SCRIPT="$CURRENT_DIR/bin/tabby hook kill-pane"

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
TITLE_FORMAT=$(grep -A3 "^terminal_title:" "$CONFIG_FILE" 2>/dev/null | grep "format:" | sed "s/.*format: *//; s/^['\"]//; s/['\"] *$//")
TITLE_FORMAT=${TITLE_FORMAT:-"tmux #{window_index}.#{pane_index} #{window_name} #{pane_current_command}"}

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
    tmux bind-key -T root MouseDown2Status run-shell "$CURRENT_DIR/bin/tabby hook kill-window #{window_index}"
    tmux bind-key -T root MouseDown3Status command-prompt -I "#W" "rename-window '%%' ; set-window-option @tabby_name_locked 1"
    tmux bind-key -T root MouseDown1StatusRight new-window
fi

# Signal sidebar refresh: now inline via SIGNAL_CMD (USR1 to daemon PID)
SIGNAL_SIDEBAR_SCRIPT="$SIGNAL_CMD"

# Refresh status bar: trivial inline command (replaces refresh_status.sh)
REFRESH_STATUS_SCRIPT="tmux refresh-client -S"

# All lifecycle scripts now handled by Go binaries via tabby-hook
HOOK_BIN="$CURRENT_DIR/bin/tabby hook"
ENSURE_SIDEBAR_CMD="$HOOK_BIN ensure-sidebar"
RESTORE_SIDEBAR_CMD="$HOOK_BIN ensure-sidebar"
STABILIZE_CLIENT_RESIZE_CMD="$HOOK_BIN stabilize-client-resize"
SIGNAL_CLIENT_RESIZE_CMD="$HOOK_BIN signal-client-resize"
FOCUS_RECOVERY_CMD="$HOOK_BIN restore-input-focus"

# Apply group to new window: now handled by daemon (createNewWindowInCurrentGroup)

# Window kill and exit-if-no-main: now handled by tabby-hook -> daemon
KILL_WINDOW_SCRIPT="$CURRENT_DIR/bin/tabby hook kill-window"
EXIT_IF_NO_MAIN_WINDOWS_CMD="$CURRENT_DIR/bin/tabby hook exit-if-no-main"

# --- Hook registrations ---
# Most hooks now signal the daemon (USR1) which handles all state internally:
# pane dimming, window history, layout save, border color, status exclusivity,
# sidebar spawning, and renderer management.
SIGNAL_CMD="$CURRENT_DIR/scripts/signal-daemon.sh"

tmux set-hook -g window-linked "run-shell -b '$SIGNAL_CMD; tmux refresh-client -S'"
tmux set-hook -g window-unlinked "run-shell -b '$SIGNAL_CMD; tmux refresh-client -S; $EXIT_IF_NO_MAIN_WINDOWS_CMD'"
tmux set-hook -g after-new-window "run-shell -b '$SIGNAL_CMD; tmux refresh-client -S'"
tmux set-hook -g after-resize-pane "run-shell -b '$HOOK_BIN on-pane-resize \"#{hook_pane}\"'"
tmux set-hook -g after-select-window "run-shell -b '$SIGNAL_CMD; tmux refresh-client -S; $ENSURE_SIDEBAR_CMD \"#{session_id}\" \"#{window_id}\"; $CYCLE_PANE_BIN --ensure-content'"

# Lock window name on manual rename via prefix+, keybinding
# NOTE: We intentionally do NOT use after-rename-window hook because the daemon's
# own rename-window calls would trigger it, locking the daemon out of future updates.
# Instead, we set @tabby_name_locked directly in each user-facing rename path.
tmux bind-key , command-prompt -I "#W" "rename-window '%%' ; set-window-option @tabby_name_locked 1"

tmux set-hook -g after-select-pane "run-shell -b '$SIGNAL_CMD'"
# after-split-window: daemon handles window name preservation (PreserveWindowNames)
tmux set-hook -g after-split-window "run-shell -b '$SIGNAL_CMD'"

# When a pane is killed: preserve ratios synchronously (must happen before tmux
# reflows), then signal daemon in background. The daemon's USR1 handler takes
# care of orphan cleanup and sidebar spawning.
PRESERVE_RATIOS_CMD="$HOOK_BIN preserve-pane-ratios"
tmux set-hook -g after-kill-pane "run-shell '$PRESERVE_RATIOS_CMD \"#{window_id}\"'; run-shell -b '$SIGNAL_CMD; $EXIT_IF_NO_MAIN_WINDOWS_CMD'"

# Restore sidebar when client reattaches to session
tmux set-hook -g client-attached "run-shell '$RESTORE_SIDEBAR_CMD'; run-shell '$STABILIZE_CLIENT_RESIZE_CMD \"#{session_id}\" \"#{window_id}\" \"#{client_tty}\" \"#{client_width}\" \"#{client_height}\"'; run-shell -b '$CYCLE_PANE_BIN --ensure-content'"

# Client resize: resize windows to client geometry, signal daemon
tmux set-hook -g client-active "run-shell '$SIGNAL_CLIENT_RESIZE_CMD \"#{client_width}\" \"#{client_height}\"'; run-shell '$ENSURE_SIDEBAR_CMD \"#{session_id}\" \"#{window_id}\"'; run-shell -b '$CYCLE_PANE_BIN --ensure-content'"
tmux set-hook -g client-focus-in "run-shell '$SIGNAL_CLIENT_RESIZE_CMD \"#{client_width}\" \"#{client_height}\"'; run-shell '$ENSURE_SIDEBAR_CMD \"#{session_id}\" \"#{window_id}\"'; run-shell -b '$CYCLE_PANE_BIN --ensure-content'"

# session-created: daemon handles sidebar spawning via USR1
tmux set-hook -g session-created "run-shell -b '$SIGNAL_CMD'"

# Maintain sidebar width after terminal resize
tmux set-hook -g client-resized "run-shell '$SIGNAL_CLIENT_RESIZE_CMD \"#{client_width}\" \"#{client_height}\"'; run-shell '$ENSURE_SIDEBAR_CMD \"#{session_id}\" \"#{window_id}\"'"

# tmux-resurrect integration (options are inert if resurrect is not installed)
RESURRECT_SAVE_CMD="$HOOK_BIN resurrect-save"
RESURRECT_RESTORE_CMD="$HOOK_BIN resurrect-restore"

EXISTING_SAVE_HOOK=$(tmux show-option -gqv @resurrect-hook-post-save-layout 2>/dev/null || echo "")
if [ -z "$EXISTING_SAVE_HOOK" ] || echo "$EXISTING_SAVE_HOOK" | grep -q "tabby"; then
    tmux set-option -g @resurrect-hook-post-save-layout "$RESURRECT_SAVE_CMD"
fi

EXISTING_RESTORE_HOOK=$(tmux show-option -gqv @resurrect-hook-post-restore-all 2>/dev/null || echo "")
if [ -z "$EXISTING_RESTORE_HOOK" ] || echo "$EXISTING_RESTORE_HOOK" | grep -q "tabby"; then
    tmux set-option -g @resurrect-hook-post-restore-all "$RESURRECT_RESTORE_CMD"
fi

# Keep tmux native chooser shortcuts available
tmux bind-key w choose-tree -Zw
tmux bind-key s choose-tree -Zs

# Configure sidebar toggle keybinding
TOGGLE_KEY=$(grep "toggle_sidebar:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "prefix + Tab")
KEY=${TOGGLE_KEY##*+ }
if [ -z "$KEY" ]; then KEY="Tab"; fi

tmux bind-key "$KEY" run-shell -b "$CURRENT_DIR/bin/tabby toggle"

# Double-click on pane or border: pass through mouse events normally
tmux bind-key -T root DoubleClick1Pane \
    "select-pane -t = ; if-shell -F '#{||:#{pane_in_mode},#{mouse_any_flag}}' { send-keys -M } { copy-mode -H ; send-keys -X select-word ; run-shell -d 0.3 ; send-keys -X copy-pipe-and-cancel }"

normalize_global_key() {
	local key="$1"
	if [[ "$key" == cmd+shift+[ ]] || [[ "$key" == cmd+[ ]]; then
		echo "M-{"
		return
	fi
	if [[ "$key" == cmd+shift+] ]] || [[ "$key" == cmd+] ]]; then
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
bind_from_config "$NEW_WINDOW_BINDING" "run-shell '$NEW_WINDOW_SCRIPT #{client_tty}'"
bind_from_config "$KILL_WINDOW_BINDING" "run-shell '$KILL_WINDOW_SCRIPT #{window_index}'"

# Swap/cycle active pane within current window (skips utility panes, signals daemon)
# Uses Go binary: bin/cycle-pane (also handles dimming)
SWAP_PANE_BINDING=$(grep "swap_pane:" "$CONFIG_FILE" 2>/dev/null | awk -F': ' '{print $2}' | sed 's/"//g' || echo "")
if [ -n "$SWAP_PANE_BINDING" ] && [ -x "$TABBY_CYCLE_BIN" ]; then
    SWAP_KEY=$(normalize_global_key "$SWAP_PANE_BINDING")
    [ -n "$SWAP_KEY" ] && tmux bind-key -n "$SWAP_KEY" run-shell "$CYCLE_PANE_BIN"
fi
# Also override prefix+o to use the smart cycle binary
if [ -x "$TABBY_CYCLE_BIN" ]; then
    tmux bind-key o run-shell "$CYCLE_PANE_BIN"
fi

# Optional: Also bind to a prefix-less key for quick access
# tmux bind-key -n M-Tab run-shell "$CURRENT_DIR/bin/tabby-toggle"

# New Group shortcut (prefix + G)
tmux bind-key G command-prompt -p 'New group name:' "run-shell '$CURRENT_DIR/bin/tabby hook new-group %%'"

# Override kill-pane to save layout first (preserves pane ratios)
tmux bind-key x confirm-before -p 'Close pane? (y/n)' "run-shell '$KILL_PANE_SCRIPT'"
tmux bind-key '&' confirm-before -p 'Close window? (y/n)' "run-shell '$KILL_WINDOW_SCRIPT #{window_index}'"

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
tmux bind-key -n M-n run-shell "$NEW_WINDOW_SCRIPT '#{client_tty}'"
tmux bind-key -n M-N run-shell "$NEW_WINDOW_SCRIPT '#{client_tty}'"
tmux unbind-key -n M-x 2>/dev/null || true
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
# Run ASYNC (-b) to avoid blocking tmux startup on cold boot.
# The daemon was pre-started at the top of this file, so by now the socket
# should be ready (or nearly ready).  Hooks (session-created, after-new-window,
# after-select-window) also call ensure_sidebar, providing redundancy.
tmux run-shell -b "$ENSURE_SIDEBAR_CMD \"#{session_id}\" \"#{window_id}\""

# Status exclusivity enforcement moved to daemon (EnforceStatusExclusivity)
tmux run-shell -b "$FOCUS_RECOVERY_CMD \"#{session_id}\""

# -----------------------------------------------------------------------------
# Mouse-drag copy -> system clipboard via OSC 52.
# scripts/osc52-copy writes an OSC 52 escape directly to the pane's pty, which
# tmux allow-passthrough forwards to the outer terminal. Works on Mac-local
# (Ghostty/iTerm) and through SSH + nested tmux.
#
# Bound AFTER all tabby tab/sidebar bindings so plugin overrides can't
# clobber MouseDragEnd1Pane. Also sets copy-command so any `copy-pipe*`
# invocation without an explicit command uses the same path.
# -----------------------------------------------------------------------------
tmux bind-key -T copy-mode    MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel "$CURRENT_DIR/scripts/osc52-copy \"#{pane_tty}\""
tmux bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel "$CURRENT_DIR/scripts/osc52-copy \"#{pane_tty}\""
tmux set-option -g copy-command "$CURRENT_DIR/scripts/osc52-copy"
