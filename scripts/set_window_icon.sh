#!/usr/bin/env bash
set -eu

# Backward-compatible wrapper.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/set_window_marker.sh" "$@"
