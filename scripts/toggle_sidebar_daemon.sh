#!/usr/bin/env bash
# Toggle tabby sidebar with daemon-based rendering
# One daemon process + lightweight renderers in each window

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && cd .. >/dev/null 2>&1 && pwd)"
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE_FILE="/tmp/tabby-sidebar-${SESSION_ID}.state"
DAEMON_SOCK="/tmp/tabby-daemon-${SESSION_ID}.sock"
DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
DAEMON_EVENTS_LOG="/tmp/tabby-daemon-${SESSION_ID}-events.log"

# --- Concurrency guard: prevent overlapping toggles (run-shell -b can fire multiple) ---
TOGGLE_LOCK="/tmp/tabby-toggle-${SESSION_ID}.lock"
if ! mkdir "$TOGGLE_LOCK" 2>/dev/null; then
    # Another toggle is already running — bail out silently
    exit 0
fi
trap 'rmdir "$TOGGLE_LOCK" 2>/dev/null || true' EXIT

# --- Global timeout: kill this script if it takes too long (prevents zombie blocking) ---
( sleep 15 && kill $$ 2>/dev/null ) &
TIMEOUT_PID=$!
trap 'rmdir "$TOGGLE_LOCK" 2>/dev/null || true; kill $TIMEOUT_PID 2>/dev/null || true' EXIT

reset_mouse_escape_sequences() {
    # BubbleTea enables terminal mouse tracking via escape sequences (1003h, 1006h).
    # If a renderer is killed before BubbleTea restores the terminal, these sequences
    # leak to the client TTY, causing permanent input loss until detach+reattach.
    # Fix: write disable sequences directly to each client TTY after killing renderers.
    for client_tty in $(tmux list-clients -F "#{client_tty}" 2>/dev/null); do
        [ -w "$client_tty" ] || continue
        printf '\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1006l\033[?1015l' > "$client_tty" 2>/dev/null || true
    done
}

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
        printf "[event] %s RESTART_REQUEST reason=unresponsive source=toggle\n" "$(date '+%Y/%m/%d %H:%M:%S')" >> "$DAEMON_EVENTS_LOG" 2>/dev/null || true
        kill "$DAEMON_PID" 2>/dev/null || true
        rm -f "$DAEMON_PID_FILE" "$DAEMON_SOCK"
    fi
}

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
WATCHDOG_SCRIPT="$CURRENT_DIR/scripts/watchdog_daemon.sh"
CLEAN_STOP_SENTINEL="/tmp/tabby-daemon-${SESSION_ID}.clean-stop"
WATCHDOG_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.watchdog.pid"

# Check if daemon binaries exist
# Note: Old sidebar code archived in .archive/old-sidebar/ (not loaded by default)
if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$RENDERER_BIN" ]; then
    echo "Error: Daemon binaries not found. Run 'make build' first." >&2
    exit 1
fi

# Get current state from tmux option (most reliable) or state file
CURRENT_STATE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")
if [ -z "$CURRENT_STATE" ] && [ -f "$SIDEBAR_STATE_FILE" ]; then
    CURRENT_STATE=$(cat "$SIDEBAR_STATE_FILE" 2>/dev/null || echo "")
fi

if [ "$CURRENT_STATE" = "enabled" ]; then
    restart_daemon_if_unresponsive
fi

# If state says "enabled" but the daemon is no longer running, treat it as a restart:
# clean up any lingering system panes and fall through to the ENABLE path.
if [ "$CURRENT_STATE" = "enabled" ]; then
    DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
    if [ -z "$DAEMON_PID" ] || ! kill -0 "$DAEMON_PID" 2>/dev/null; then
        CURRENT_STATE="disabled"
        tmux set-option @tabby_sidebar "disabled" 2>/dev/null || true
        while IFS= read -r line; do
            [ -z "$line" ] && continue
            pane_id=$(echo "$line" | cut -d'|' -f2)
            tmux kill-pane -t "$pane_id" 2>/dev/null || true
        done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabby-daemon|pane-header)" || true)
    fi
fi

if [ "$CURRENT_STATE" = "enabled" ]; then
    # === DISABLE SIDEBARS ===

    # Write sentinel so the watchdog knows this is an intentional stop
    echo $$ > "$CLEAN_STOP_SENTINEL"

    # Kill daemon if running
    if [ -f "$DAEMON_PID_FILE" ]; then
        DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$DAEMON_PID" ]; then
            kill "$DAEMON_PID" 2>/dev/null || true
        fi
        rm -f "$DAEMON_PID_FILE"
    fi
    rm -f "$DAEMON_SOCK"

    # Kill watchdog if running
    if [ -f "$WATCHDOG_PID_FILE" ]; then
        WATCHDOG_PID=$(cat "$WATCHDOG_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$WATCHDOG_PID" ] && kill -0 "$WATCHDOG_PID" 2>/dev/null; then
            kill "$WATCHDOG_PID" 2>/dev/null || true
        fi
        rm -f "$WATCHDOG_PID_FILE"
    fi

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

    # Brief wait for renderers to start graceful cleanup
    sleep 0.1

    reset_mouse_escape_sequences

    # Now kill the panes
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabby-daemon|pane-header)" || true)

    tmux set -g mouse off 2>/dev/null || true
    sleep 0.1
    tmux set -g mouse on 2>/dev/null || true
    tmux refresh-client -S 2>/dev/null || true

    # Remove tmux hooks for resize events
    tmux set-hook -gu after-resize-pane 2>/dev/null || true
    tmux set-hook -gu after-resize-window 2>/dev/null || true
    tmux set-hook -gu client-resized 2>/dev/null || true
    
    # Remove after-select-window hook to prevent auto-relaunch
    # The hook calls ensure_sidebar.sh which would re-enable the sidebar
    tmux set-hook -gu after-select-window 2>/dev/null || true

    echo "disabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tabby_sidebar "disabled"
    tmux set-option -g status on
else
    # === ENABLE SIDEBARS ===
    echo "enabled" > "$SIDEBAR_STATE_FILE"
    tmux set-option @tabby_sidebar "enabled"

    # Snapshot saved pane layouts before system panes are killed/re-spawned.
    # after-split-window will overwrite @tabby_layout_* when new system panes are
    # added, so we preserve them here for restoration after spawn completes.
    while IFS= read -r window_id; do
        [ -z "$window_id" ] && continue
        saved=$(tmux show-option -gqv "@tabby_layout_${window_id}" 2>/dev/null || true)
        [ -n "$saved" ] && tmux set-option -g "@tabby_restore_layout_${window_id}" "$saved" 2>/dev/null || true
    done < <(tmux list-windows -F "#{window_id}" 2>/dev/null || true)

    # Close any existing sidebar/renderer panes first (gracefully with SIGTERM)
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        pane_pid=$(echo "$line" | cut -d'|' -f3)
        # Send SIGTERM first to allow cleanup
        if [ -n "$pane_pid" ]; then
            kill -TERM "$pane_pid" 2>/dev/null || true
        fi
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}|#{pane_pid}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|pane-header)" || true)

    # Brief wait for cleanup
    sleep 0.1

    reset_mouse_escape_sequences

    # Now kill the panes
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        tmux kill-pane -t "$pane_id" 2>/dev/null || true
    done < <(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" 2>/dev/null | grep -E "^(sidebar|sidebar-renderer|tabby-daemon|pane-header)" || true)

    tmux set-option -g status off

    # Save current window and pane before making changes
    CURRENT_WINDOW=$(tmux display-message -p '#{window_id}')
    CURRENT_PANE=$(tmux display-message -p '#{pane_id}')
    tmux set-option -g @tabby_last_window "$CURRENT_WINDOW"
    tmux set-option -g @tabby_last_pane "$CURRENT_PANE"

    if [ "${TABBY_DEBUG:-}" = "1" ]; then
        "$WATCHDOG_SCRIPT" -session "$SESSION_ID" -debug &
    else
        "$WATCHDOG_SCRIPT" -session "$SESSION_ID" &
    fi

    SOCKET_READY=false
    for _ in $(seq 1 20); do
        if [ -S "$DAEMON_SOCK" ]; then
            SOCKET_READY=true
            break
        fi
        sleep 0.02
    done

    if [ "$SOCKET_READY" = "false" ]; then
        echo "Error: Failed to start daemon (socket not created)" >&2
        exit 1
    fi

    # Store daemon PID in tmux option for hooks to find dynamically
    DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
    tmux set-option -g @tabby_daemon_pid "$DAEMON_PID"

    # Restore tmux hooks to match what tabby.tmux originally sets.
    # The disable path removes these, so we must put back the full versions
    # (not just USR1-only stubs) to keep signal_sidebar + ensure_sidebar working.
    SIGNAL_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/signal_sidebar.sh"
    ENSURE_SIDEBAR_SCRIPT="$CURRENT_DIR/scripts/ensure_sidebar.sh"
    STATUS_GUARD_SCRIPT="$CURRENT_DIR/scripts/enforce_status_exclusivity.sh"
    # shellcheck disable=SC2016
    tmux set-hook -g after-resize-pane 'run-shell -b "kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true"'
    # shellcheck disable=SC2016
    tmux set-hook -g after-resize-window 'run-shell -b "kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true"'
    tmux set-hook -g client-resized "run-shell '$SIGNAL_SIDEBAR_SCRIPT'; run-shell '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell '$STATUS_GUARD_SCRIPT \"#{session_id}\"'"
    
    # Restore after-select-window hook (matches tabby.tmux line 581)
    ON_WINDOW_SELECT_SCRIPT="$CURRENT_DIR/scripts/on_window_select.sh"
    REFRESH_STATUS_SCRIPT="$CURRENT_DIR/scripts/refresh_status.sh"
    TRACK_WINDOW_HISTORY_SCRIPT="$CURRENT_DIR/scripts/track_window_history.sh"
    CYCLE_PANE_BIN="$CURRENT_DIR/bin/cycle-pane"
    tmux set-hook -g after-select-window "run-shell '$ON_WINDOW_SELECT_SCRIPT'; run-shell '$REFRESH_STATUS_SCRIPT'; run-shell -b '$TRACK_WINDOW_HISTORY_SCRIPT'; run-shell -b '$ENSURE_SIDEBAR_SCRIPT \"#{session_id}\" \"#{window_id}\"'; run-shell -b '$STATUS_GUARD_SCRIPT \"#{session_id}\"'; run-shell -b '[ -x \"$CYCLE_PANE_BIN\" ] && \"$CYCLE_PANE_BIN\" --dim-only'"

    # Brief wait for daemon to start spawning renderers, then let it work asynchronously.
    # The daemon will spawn renderers via its ticker loop, and ensure_sidebar.sh will
    # catch any missing ones. No need to block the toggle for full renderer startup.
    RENDERER_WAIT_MAX=10  # 10 * 0.05s = 0.5s max
    RENDERER_WAIT_COUNT=0
    RENDERERS_READY=false
    
    while [ $RENDERER_WAIT_COUNT -lt $RENDERER_WAIT_MAX ]; do
        RENDERER_COUNT=$(tmux list-panes -s -F "#{pane_current_command}|#{pane_start_command}" 2>/dev/null | \
            grep -cE "(sidebar-renderer|sidebar)" || true)
        RENDERER_COUNT="${RENDERER_COUNT:-0}"
        RENDERER_COUNT=$(echo "$RENDERER_COUNT" | tr -d '[:space:]')
        
        if [ "$RENDERER_COUNT" -gt 0 ]; then
            RENDERERS_READY=true
            break
        fi
        
        sleep 0.05
        RENDERER_WAIT_COUNT=$((RENDERER_WAIT_COUNT + 1))
    done
    
    if [ "${TABBY_DEBUG:-}" = "1" ]; then
        echo "Renderers ready: $RENDERERS_READY (waited ${RENDERER_WAIT_COUNT}*0.05s)" >&2
    fi

    # Restore content pane layouts — re-spawning system panes disrupts saved ratios.
    while IFS= read -r window_id; do
        [ -z "$window_id" ] && continue
        restore_layout=$(tmux show-option -gqv "@tabby_restore_layout_${window_id}" 2>/dev/null || true)
        if [ -n "$restore_layout" ]; then
            tmux select-layout -t "$window_id" "$restore_layout" 2>/dev/null || true
            tmux set-option -g "@tabby_layout_${window_id}" "$restore_layout" 2>/dev/null || true
            tmux set-option -gu "@tabby_restore_layout_${window_id}" 2>/dev/null || true
        fi
    done < <(tmux list-windows -F "#{window_id}" 2>/dev/null || true)

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

# Refresh status bar for all clients
for client_tty in $(tmux list-clients -F "#{client_tty}" 2>/dev/null); do
    tmux refresh-client -t "$client_tty" -S 2>/dev/null || true
done
