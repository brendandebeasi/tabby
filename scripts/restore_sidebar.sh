#!/usr/bin/env bash
# Restore sidebar/tabbar state when client attaches to session
# Uses tmux user option @tmux-tabs-sidebar for persistence across reattach
#
# Architecture: 1 daemon per session + 1 renderer per window
# This script ensures the daemon is running and renderers exist in all windows.

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"

# Check tmux user option for persistent state (survives detach/reattach)
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")

# Also check temp file as fallback
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

# Get saved sidebar width or default
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width)
if [ -z "$SIDEBAR_WIDTH" ]; then SIDEBAR_WIDTH=25; fi

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"

if [ "$MODE" = "enabled" ]; then
    # Vertical sidebar mode using daemon architecture
    # Ensure daemon is running, then ensure each window has exactly one renderer

    # Check if daemon is alive
    DAEMON_RUNNING=false
    if [ -S "$DAEMON_SOCK" ]; then
        # Socket exists, check if process is alive
        if [ -f "$DAEMON_PID_FILE" ]; then
            DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
            if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
                DAEMON_RUNNING=true
            fi
        fi
    fi

    # Start daemon if not running
    if [ "$DAEMON_RUNNING" = "false" ]; then
        # Clean up stale files
        rm -f "$DAEMON_SOCK" "$DAEMON_PID_FILE"

        # Start daemon
        if [ "${TABBY_DEBUG:-}" = "1" ]; then
            "$DAEMON_BIN" -session "$SESSION_ID" -debug &
        else
            "$DAEMON_BIN" -session "$SESSION_ID" &
        fi

        # Wait for socket
        for i in $(seq 1 20); do
            if [ -S "$DAEMON_SOCK" ]; then
                break
            fi
            sleep 0.1
        done
    fi

    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')
    DEBUG_FLAG=""
    if [ "${TABBY_DEBUG:-}" = "1" ]; then
        DEBUG_FLAG="-debug"
    fi

    # Iterate all windows
    tmux list-windows -F "#{window_id}" | while read -r window_id; do
        [ -z "$window_id" ] && continue

        # Get all sidebar/renderer panes in this window
        SIDEBAR_PANES=$(tmux list-panes -t "$window_id" -F "#{pane_id}:#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -E "(sidebar|sidebar-renderer)" | cut -d: -f1 || true)

        # Count them
        COUNT=$(echo "$SIDEBAR_PANES" | grep -c "^%" 2>/dev/null || echo "0")

        if [ "$COUNT" -eq 0 ]; then
            # No renderer in this window - spawn one with proper flags
            FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
            if [ -n "$FIRST_PANE" ]; then
                tmux split-window -t "$FIRST_PANE" -h -b -l "$SIDEBAR_WIDTH" \
                    "exec '$RENDERER_BIN' -session '$SESSION_ID' -window '$window_id' $DEBUG_FLAG" 2>/dev/null || true

                # Hide border for sidebar pane and prevent dimming
                SIDEBAR_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}:#{pane_start_command}" 2>/dev/null | grep "sidebar" | cut -d: -f1 || echo "")
                if [ -n "$SIDEBAR_PANE" ]; then
                    tmux set-option -p -t "$SIDEBAR_PANE" pane-border-status off 2>/dev/null || true
                    tmux select-pane -t "$SIDEBAR_PANE" -P 'bg=default' 2>/dev/null || true
                fi
            fi
        elif [ "$COUNT" -gt 1 ]; then
            # Duplicates - kill all and respawn ONE
            for pane in $SIDEBAR_PANES; do
                [ -n "$pane" ] && tmux kill-pane -t "$pane" 2>/dev/null || true
            done

            FIRST_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}" 2>/dev/null | head -1)
            if [ -n "$FIRST_PANE" ]; then
                tmux split-window -t "$FIRST_PANE" -h -b -l "$SIDEBAR_WIDTH" \
                    "exec '$RENDERER_BIN' -session '$SESSION_ID' -window '$window_id' $DEBUG_FLAG" 2>/dev/null || true

                SIDEBAR_PANE=$(tmux list-panes -t "$window_id" -F "#{pane_id}:#{pane_start_command}" 2>/dev/null | grep "sidebar" | cut -d: -f1 || echo "")
                if [ -n "$SIDEBAR_PANE" ]; then
                    tmux set-option -p -t "$SIDEBAR_PANE" pane-border-status off 2>/dev/null || true
                    tmux select-pane -t "$SIDEBAR_PANE" -P 'bg=default' 2>/dev/null || true
                fi
            fi
        fi
        # If COUNT == 1, correct state - do nothing
    done

    # Return to original window and focus main pane (right side)
    tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
    tmux select-pane -t "{right}" 2>/dev/null || true

elif [ "$MODE" = "horizontal" ]; then
    # Horizontal tabbar mode
    tmux set-option -g status off

    # Note: grep -c outputs count (0 if no match) but exits 1 on no match
    TABBAR_COUNT=$(tmux list-panes -s -F "#{pane_current_command}" 2>/dev/null | grep -c "^tabbar$" || true)

    if [ "$TABBAR_COUNT" -eq 0 ]; then
        # Restore tabbars in all windows
        CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')

        tmux list-windows -F "#{window_id}" | while read -r window_id; do
            if ! tmux list-panes -t "$window_id" -F "#{pane_current_command}" 2>/dev/null | grep -q "^tabbar$"; then
                tmux split-window -t "$window_id" -v -b -l 1 "exec \"$CURRENT_DIR/bin/tabbar\"" 2>/dev/null || true
            fi
        done

        # Return to original window and focus main pane
        tmux select-window -t "$CURRENT_WINDOW" 2>/dev/null || true
        tmux select-pane -t "{down}" 2>/dev/null || true
    fi
fi
