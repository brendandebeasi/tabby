#!/usr/bin/env bash
# Toggle tabby sidebar using daemon-renderer architecture

set -eu

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

# Use daemon mode (only supported mode now)
DAEMON_BIN="$CURRENT_DIR/bin/tabby-daemon"
RENDERER_BIN="$CURRENT_DIR/bin/sidebar-renderer"

if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$RENDERER_BIN" ]; then
    echo "Error: Daemon binaries not found. Run 'make build' first." >&2
    exit 1
fi

# Delegate to daemon-specific toggle script
exec "$CURRENT_DIR/scripts/toggle_sidebar_daemon.sh"
