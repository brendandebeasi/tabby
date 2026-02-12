#!/usr/bin/env bash
# Dev reload workflow: rebuild binaries and restart sidebar if enabled.
# Fails loudly when runtime daemon is stale after reload.
# Opt-in via @tabby_dev_reload_enabled tmux option or TABBY_DEV_RELOAD env var
# Does NOT change sidebar state if disabled - only restarts if already enabled

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Check if dev reload is enabled (opt-in)
DEV_RELOAD_ENABLED="${TABBY_DEV_RELOAD:-}"
if [ -z "$DEV_RELOAD_ENABLED" ]; then
    DEV_RELOAD_ENABLED=$(tmux show-option -gqv @tabby_dev_reload_enabled 2>/dev/null || echo "")
fi

if [ "$DEV_RELOAD_ENABLED" != "1" ] && [ "$DEV_RELOAD_ENABLED" != "true" ]; then
    tmux display-message -d 3000 "Tabby: dev reload disabled (@tabby_dev_reload_enabled 1)" 2>/dev/null || true
    exit 0
fi

# Get current sidebar state before rebuild
SESSION_ID=$(tmux display-message -p '#{session_id}')
SIDEBAR_STATE=$(tmux show-options -qv @tabby_sidebar 2>/dev/null || echo "")

# Rebuild binaries
tmux display-message -d 2000 "Tabby: rebuilding binaries..." 2>/dev/null || true
if ! "$CURRENT_DIR/scripts/install.sh" >/dev/null 2>&1; then
    tmux display-message -d 3000 "Tabby: build failed (see scripts/install.sh)" 2>/dev/null || true
    exit 1
fi

tmux display-message -d 2000 "Tabby: build complete" 2>/dev/null || true

# Only restart sidebar if it was enabled before rebuild
if [ "$SIDEBAR_STATE" = "enabled" ]; then
    echo "Tabby: restarting sidebar..."
    
    # Kill daemon if running
    DAEMON_PID_FILE="/tmp/tabby-daemon-${SESSION_ID}.pid"
    if [ -f "$DAEMON_PID_FILE" ]; then
        DAEMON_PID=$(cat "$DAEMON_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$DAEMON_PID" ]; then
            kill "$DAEMON_PID" 2>/dev/null || true
        fi
        rm -f "$DAEMON_PID_FILE"
    fi
    
    # Close all sidebar and renderer panes (gracefully)
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        pane_id=$(echo "$line" | cut -d'|' -f2)
        pane_pid=$(echo "$line" | cut -d'|' -f3)
        # Send SIGTERM to renderer process first (allows graceful cleanup)
        if [ -n "$pane_pid" ]; then
            kill -TERM "$pane_pid" 2>/dev/null || true
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
    
    # Reset mouse state
    tmux set -g mouse off 2>/dev/null || true
    sleep 0.1
    tmux set -g mouse on 2>/dev/null || true
    
    # Re-enable sidebar using toggle script
    sleep 0.5
    if ! "$CURRENT_DIR/scripts/toggle_sidebar_daemon.sh" >/dev/null 2>&1; then
        tmux display-message -d 4000 "Tabby: reload failed (could not restart daemon)" 2>/dev/null || true
        exit 1
    fi

    # Verify daemon freshness for this session and fail loudly if stale.
    set +e
    STATUS_OUTPUT="$($CURRENT_DIR/scripts/dev-status.sh "$SESSION_ID" 2>&1)"
    STATUS_RC=$?
    set -e
    if [ "$STATUS_RC" -ne 0 ]; then
        tmux display-message -d 5000 "Tabby: reload failed (stale runtime)" 2>/dev/null || true
        printf '%s\n' "$STATUS_OUTPUT"
        exit 1
    fi

    tmux display-message -d 2000 "Tabby: reload complete" 2>/dev/null || true
else
    tmux display-message -d 2000 "Tabby: rebuild complete (sidebar disabled)" 2>/dev/null || true
fi
