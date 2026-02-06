#!/usr/bin/env bash
# Toggle tmux-tabs sidebar with daemon-based rendering
# One daemon process + lightweight renderers in each window

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && cd .. >/dev/null 2>&1 && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"

# Get saved sidebar width or default
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width)
if [ -z "$SIDEBAR_WIDTH" ]; then SIDEBAR_WIDTH=25; fi

# Get sidebar position and mode
SIDEBAR_POSITION=$(tmux show-option -gqv @tabby_sidebar_position)
if [ -z "$SIDEBAR_POSITION" ]; then SIDEBAR_POSITION="left"; fi

SIDEBAR_MODE=$(tmux show-option -gqv @tabby_sidebar_mode)
if [ -z "$SIDEBAR_MODE" ]; then SIDEBAR_MODE="full"; fi

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"

# Check if daemon binaries exist
# Note: Old sidebar code archived in .archive/old-sidebar/ (not loaded by default)
if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$RENDERER_BIN" ]; then
    echo "Error: Daemon binaries not found. Run 'make build' first." >&2
    exit 1
fi

# Get current state from tmux option (most reliable) or state file
CURRENT_STATE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$CURRENT_STATE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    CURRENT_STATE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$CURRENT_STATE" = "enabled" ]; then
    # === DISABLE SIDEBARS ===

    # Kill daemon if running
    if [ -f "$DAEMON_PID_FILE" ]; then
        DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$DAEMON_PID" ]; then
            kill "$DAEMON_PID" 2>/dev/null || true
        fi
        rm -f "$DAEMON_PID_FILE"
    fi
    rm -f "$DAEMON_SOCK"

    # Close all sidebar and renderer panes in session (gracefully)
    RENDERER_PIDS=""
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        pane_pid=$(echo "$line" | cut -d'|' -f3)
        # Send SIGTERM to renderer process first (allows graceful cleanup)
        if [ -n "$pane_pid" ]; then
            kill -TERM "$pane_pid" 2>/dev/null || true
            RENDERER_PIDS="$RENDERER_PIDS $pane_pid"
        fi
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}|#{pane_pid}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|pane-header)" || true)

    # Wait for renderers to cleanup gracefully
    sleep 0.5

    # Now kill the panes
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabby-daemon|pane-header)" || true)

    # Force reset tmux's mouse state by toggling it off/on
    # This clears any corrupted internal mouse tracking state
    tmux set -g mouse off 2>/dev/null || true
    sleep 0.1
    tmux set -g mouse on 2>/dev/null || true
    tmux refresh-client -S 2>/dev/null || true

    # Remove tmux hooks for resize events
    tmux set-hook -gu after-resize-pane 2>/dev/null || true
    tmux set-hook -gu after-resize-window 2>/dev/null || true
    tmux set-hook -gu client-resized 2>/dev/null || true

    echo "disabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "disabled"
    tmux set-option -g status on
else
    # === ENABLE SIDEBARS ===
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "enabled"

    # Close any existing sidebar/renderer panes first (gracefully with SIGTERM)
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        pane_pid=$(echo "$line" | cut -d'|' -f3)
        # Send SIGTERM first to allow cleanup
        if [ -n "$pane_pid" ]; then
            kill -TERM "$pane_pid" 2>/dev/null || true
        fi
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}|#{pane_pid}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabbar|pane-header)" || true)

    # Wait for cleanup
    sleep 0.5

    # Now kill the panes
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabbar|pane-header)" || true)

    tmux set-option -g status off

    # Save current window and pane before making changes
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')
    CURRENT_PANE=$(tmux display-message -p '#{pane_id}')
    tmux set-option -g @tabby_last_window "$CURRENT_WINDOW"
    tmux set-option -g @tabby_last_pane "$CURRENT_PANE"

    if [ "${TABBY_DEBUG:-}" = "1" ]; then
        "$DAEMON_BIN" -session "$SESSION_ID" -debug &
    else
        "$DAEMON_BIN" -session "$SESSION_ID" &
    fi

    SOCKET_READY=false
    for i in $(seq 1 20); do
        if [ -S "$DAEMON_SOCK" ]; then
            SOCKET_READY=true
            break
        fi
        sleep 0.1
    done

    if [ "$SOCKET_READY" = "false" ]; then
        echo "Error: Failed to start daemon (socket not created)" >&2
        exit 1
    fi

    # Store daemon PID in tmux option for hooks to find dynamically
    DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
    tmux set-option -g @tabby_daemon_pid "$DAEMON_PID"

    # Set up tmux hooks to notify daemon of resize events (sends SIGUSR1)
    # These ensure background sidebars update when layouts change
    # Use tmux option to get PID dynamically (survives daemon restarts)
    # NOTE: Do NOT add after-select-window hook - it causes feedback loops when
    # clicking select_window in sidebar (click -> tmux select-window -> hook -> SIGUSR1)
    tmux set-hook -g after-resize-pane 'run-shell -b "kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true"'
    tmux set-hook -g after-resize-window 'run-shell -b "kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true"'
    tmux set-hook -g client-resized 'run-shell -b "kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true"'

    # The daemon handles spawning sidebar renderers and pane headers
    # via its windowCheckTicker loop. Just wait briefly for it to spawn them.
    sleep 1

    # Get all windows
    WINDOWS=$(tmux list-windows -F "#{window_id}")

    # Restore focus to the saved window and pane
    SAVED_WINDOW=$(tmux show-option -gqv @tabby_last_window)
    SAVED_PANE=$(tmux show-option -gqv @tabby_last_pane)

    if [ -n "$SAVED_WINDOW" ]; then
        tmux select-window -t "$SAVED_WINDOW" 2>/dev/null || true
    fi

    if [ -n "$SAVED_PANE" ]; then
        # Try to restore the exact pane
        tmux select-pane -t "$SAVED_PANE" 2>/dev/null || true
    else
        # Fallback: focus main pane based on sidebar position
        if [ "$SIDEBAR_POSITION" = "left" ]; then
            tmux select-pane -t "{right}" 2>/dev/null || true
        else
            tmux select-pane -t "{left}" 2>/dev/null || true
        fi
    fi

    # Clear activity flags that may have been set during sidebar setup
    sleep 0.1
    for window_id in $WINDOWS; do
        tmux set-window-option -t "$window_id" -q monitor-activity off 2>/dev/null || true
        tmux set-window-option -t "$window_id" -q monitor-activity on 2>/dev/null || true
    done
fi

# Force reset tmux's mouse state by toggling it off/on
# This clears any corrupted internal mouse tracking state from killed renderers
tmux set -g mouse off 2>/dev/null || true
sleep 0.1
tmux set -g mouse on 2>/dev/null || true

# Force refresh all clients individually to fix focus issues
for client_tty in $(tmux list-clients -F "#{client_tty}" 2>/dev/null); do
    tmux refresh-client -t "$client_tty" -S 2>/dev/null || true
done
