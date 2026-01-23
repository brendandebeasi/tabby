#!/usr/bin/env bash
# Toggle tmux-tabs sidebar with daemon-based rendering
# One daemon process + lightweight renderers in each window

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
SIDEBAR_WIDTH=25

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"
SIDEBAR_BIN="$CURRENT_DIR/bin/sidebar"

# Fall back to old sidebar if daemon binaries don't exist
if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$RENDERER_BIN" ]; then
    echo "Daemon binaries not found, falling back to legacy sidebar"
    exec "$CURRENT_DIR/scripts/toggle_sidebar.sh"
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

    # Close all sidebar and renderer panes in session
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabby-daemon)" || true)

    echo "disabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "disabled"
    tmux set-option -g status on
else
    # === ENABLE SIDEBARS ===
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tmux-tabs-sidebar "enabled"

    # Close any existing sidebar/renderer panes first
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabbar)" || true)

    tmux set-option -g status off

    # Get current window before making changes
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

    # Start daemon if not already running
    if [ ! -S "$DAEMON_SOCK" ]; then
        # Clean up any stale files
        rm -f "$DAEMON_SOCK"
        rm -f "$DAEMON_PID_FILE"

        # Start daemon in background (it manages its own PID file)
        "$DAEMON_BIN" -session "$SESSION_ID" &

        # Wait for socket to be ready
        SOCKET_READY=false
        for i in $(seq 1 20); do
            if [ -S "$DAEMON_SOCK" ]; then
                SOCKET_READY=true
                break
            fi
            sleep 0.1
        done

        # If socket didn't appear, daemon failed to start
        if [ "$SOCKET_READY" = "false" ]; then
            echo "Error: Failed to start daemon (socket not created)" >&2
            exit 1
        fi
    fi

    # Get all windows
    WINDOWS=$(tmux list-windows -F "#{window_id}")

    # Open renderer in all windows that don't have one
    for window_id in $WINDOWS; do
        [ -z "$window_id" ] && continue

        # Check if window already has sidebar or renderer
        if tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -qE "^(sidebar|sidebar-renderer)"; then
            continue
        fi

        FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -z "$FIRST_PANE" ]; then
            continue
        fi

        # Split window with renderer, passing the window ID for unique clientID
        tmux split-window -t "$FIRST_PANE" -h -b -f -l "$SIDEBAR_WIDTH" \
            "exec '$RENDERER_BIN' -session '$SESSION_ID' -window '$window_id'" || true
    done

    # Enforce uniform sidebar width and full window height
    for window_id in $WINDOWS; do
        SIDEBAR_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}:#{pane_current_command}" 2>/dev/null | grep -E ":(sidebar|sidebar-renderer)$" | cut -d: -f1 || echo "")
        if [ -n "$SIDEBAR_PANE" ]; then
            MAIN_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}:#{pane_current_command}" 2>/dev/null | grep -vE ":(sidebar|sidebar-renderer)$" | head -1 | cut -d: -f1 || echo "")
            if [ -n "$MAIN_PANE" ]; then
                WINDOW_HEIGHT=$(tmux display-message -t "$MAIN_PANE" -p "#{pane_height}")
                tmux resize-pane -t "$SIDEBAR_PANE" -x "$SIDEBAR_WIDTH" -y "$WINDOW_HEIGHT" 2>/dev/null || true
            fi
        fi
    done

    # Return to original window and focus main pane
    tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
    tmux select-pane -t "{right}" 2>/dev/null || true

    # Clear activity flags that may have been set during sidebar setup
    sleep 0.2
    for window_id in $WINDOWS; do
        tmux set-window-option -t "$window_id" -q monitor-activity off 2>/dev/null || true
        tmux set-window-option -t "$window_id" -q monitor-activity on 2>/dev/null || true
    done
fi

# Force refresh
tmux refresh-client -S 2>/dev/null || true
