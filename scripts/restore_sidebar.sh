#!/usr/bin/env bash
# Restore sidebar/tabbar state when client attaches to session
# Uses tmux user option @tabby_sidebar for persistence across reattach
#
# Architecture: 1 daemon per session + 1 renderer per window
# This script ensures the daemon is running and renderers exist in all windows.

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tabby-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
DAEMON_EVENTS_LOG="/tmp/tabby-daemon-${SESSION_ID}-events.log"

restart_daemon_if_unresponsive() {
    if [ ! -f "$DAEMON_PID_FILE" ] || [ ! -S "$DAEMON_SOCK" ]; then
        return
    fi

    DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
    if [ -z "$DAEMON_PID" ] || ! kill -0 "$DAEMON_PID" 2>/dev/null; then
        rm -f "$DAEMON_PID_FILE" "$DAEMON_SOCK"
        return
    fi

    if [ ! -f "$DAEMON_EVENTS_LOG" ]; then
        return
    fi

    LAST_SIZE=$(stat -f %z "$DAEMON_EVENTS_LOG" 2>/dev/null || echo "")
    if [ -z "$LAST_SIZE" ]; then
        return
    fi

    kill -USR1 "$DAEMON_PID" 2>/dev/null || true
    NEW_SIZE=""
    for _ in $(seq 1 10); do
        sleep 0.1
        NEW_SIZE=$(stat -f %z "$DAEMON_EVENTS_LOG" 2>/dev/null || echo "")
        if [ -n "$NEW_SIZE" ] && [ "$NEW_SIZE" -gt "$LAST_SIZE" ]; then
            return
        fi
    done

    if [ -n "$NEW_SIZE" ] && [ "$NEW_SIZE" = "$LAST_SIZE" ]; then
        tmux display-message -d 3000 "Tabby: daemon unresponsive, restarting" 2>/dev/null || true
        printf "[event] %s RESTART_REQUEST reason=unresponsive source=restore\n" "$(date '+%Y/%m/%d %H:%M:%S')" >> "$DAEMON_EVENTS_LOG" 2>/dev/null || true
        kill "$DAEMON_PID" 2>/dev/null || true
        rm -f "$DAEMON_SOCK" "$DAEMON_PID_FILE"
    fi
}

# Check tmux user option for persistent state (survives detach/reattach)
MODE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")

# Also check temp file as fallback
if [ -z "$MODE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    MODE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

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

if [ "$MODE" = "enabled" ]; then
	tmux set-option -g status off

	restart_daemon_if_unresponsive

	while IFS= read -r pane_id; do
		[ -n "$pane_id" ] && tmux kill-pane -t "$pane_id" 2>/dev/null || true
	done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep "^tabbar|" | cut -d'|' -f2 || true)

    # Vertical sidebar mode using daemon architecture
    # Ensure daemon is running - it handles all renderer spawning/cleanup

    # Check if daemon is alive
    DAEMON_RUNNING=false
    if [ -S "$DAEMON_SOCK" ]; then
        if [ -f "$DAEMON_PID_FILE" ]; then
            DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
            if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
                DAEMON_RUNNING=true
            fi
        fi
    fi

    # Start daemon if not running
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

    # Signal daemon for immediate refresh (spawns renderers for windows that need them)
    if [ -f "$DAEMON_PID_FILE" ]; then
        read -r PID < "$DAEMON_PID_FILE"
        kill -USR1 "$PID" 2>/dev/null || true
    fi

    # Enforce saved sidebar width on all renderer panes (fixes tiny sidebar after reattach)
    tmux list-panes -a -F '#{pane_id}:#{pane_current_command}:#{pane_start_command}' 2>/dev/null | grep -E ':(sidebar|sidebar-renderer)' | while IFS=: read -r pane_id _; do
        tmux resize-pane -t "$pane_id" -x "$SIDEBAR_WIDTH" 2>/dev/null || true
    done

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
