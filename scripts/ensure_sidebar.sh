#!/usr/bin/env bash
# Ensure sidebar/tabbar exists in current window when that mode is enabled
# Called by tmux hooks when windows are created/switched
#
# Architecture: 1 daemon per session + 1 renderer per window

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tmux-tabs-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"

# Get saved sidebar width or default
SIDEBAR_WIDTH=$(tmux show-option -gqv @tabby_sidebar_width)
if [ -z "$SIDEBAR_WIDTH" ]; then SIDEBAR_WIDTH=25; fi
TABBAR_HEIGHT=2

DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"

# Get mode from tmux option or state file
MODE=$(tmux show-options -qv @tmux-tabs-sidebar 2>/dev/null || echo "")
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$MODE" = "enabled" ]; then
    # Check if CURRENT window has a sidebar-renderer (daemon-based)
    # Check both current command and start command (for reliability during startup)
    HAS_SIDEBAR=$(tmux list-panes -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | grep -qE "(sidebar-renderer|sidebar)" && echo "yes" || echo "no")

    if [ "$HAS_SIDEBAR" = "no" ]; then
        # Verify daemon is running before spawning renderer
        DAEMON_RUNNING=false
        if [ -S "$DAEMON_SOCK" ]; then
            if [ -f "$DAEMON_PID_FILE" ]; then
                DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
                if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
                    DAEMON_RUNNING=true
                fi
            fi
        fi

        # Start daemon if needed
        if [ "$DAEMON_RUNNING" = "false" ]; then
            rm -f "$DAEMON_SOCK" "$DAEMON_PID_FILE"
            if [ "${TABBY_DEBUG:-}" = "1" ]; then
                "$DAEMON_BIN" -session "$SESSION_ID" -debug &
            else
                "$DAEMON_BIN" -session "$SESSION_ID" &
            fi
            # Wait for socket
            for i in $(seq 1 20); do
                [ -S "$DAEMON_SOCK" ] && break
                sleep 0.1
            done
        fi

        # No sidebar in current window - add renderer pane
        WINDOW_ID=$(tmux display-message -p '#{window_id}')
        MAIN_PANE=$(tmux list-panes -F "#{pane_id}" 2>/dev/null | tail -1)
        if [ -n "$MAIN_PANE" ]; then
            DEBUG_FLAG=""
            if [ "${TABBY_DEBUG:-}" = "1" ]; then
                DEBUG_FLAG="-debug"
            fi
            tmux split-window -t "$MAIN_PANE" -h -b -l "$SIDEBAR_WIDTH" \
                "exec '$RENDERER_BIN' -session '$SESSION_ID' -window '$WINDOW_ID' $DEBUG_FLAG" || true

            # Hide pane border title for sidebar and prevent dimming
            SIDEBAR_PANE=$(tmux list-panes -F "#{pane_id}:#{pane_current_command}" 2>/dev/null | grep -E "sidebar" | cut -d: -f1 || echo "")
            if [ -n "$SIDEBAR_PANE" ]; then
                tmux set-option -p -t "$SIDEBAR_PANE" pane-border-status off 2>/dev/null || true
                tmux select-pane -t "$SIDEBAR_PANE" -P 'bg=default' 2>/dev/null || true
            fi

            # Focus the main content pane (right side)
            tmux select-pane -t "{right}" 2>/dev/null || true
        fi
    fi
elif [ "$MODE" = "horizontal" ]; then
    # Check if CURRENT window has a tabbar
    TABBAR_COUNT=$(tmux list-panes -F "#{pane_current_command}" 2>/dev/null | grep -c "^tabbar$" || echo "0")

    if [ "$TABBAR_COUNT" -eq 0 ]; then
        FIRST_PANE=$(tmux list-panes -F "#{pane_id}" 2>/dev/null | head -1)
        if [ -n "$FIRST_PANE" ]; then
            tmux split-window -t "$FIRST_PANE" -v -b -l "$TABBAR_HEIGHT" "exec \"$CURRENT_DIR/bin/tabbar\"" || true
            tmux select-pane -t "{bottom}" 2>/dev/null || true
        fi
    fi
fi
