#!/usr/bin/env bash
#
# resurrect_save_hook.sh — tmux-resurrect post-save-layout hook
#
# Strips Tabby utility pane lines from the resurrect save file so that
# sidebar-renderer, pane-header, and tabby-daemon panes
# are NOT restored as zombie shells on next resurrect-restore.
#
# Usage: Called automatically by tmux-resurrect via:
#   @resurrect-hook-post-save-layout "path/to/this/script"
# Receives $1 = path to the resurrect save file.
#
# Save file format (tab-separated):
#   pane  session  win_idx  win_name  win_active  win_flags  pane_idx  dir  pane_active  pane_command  full_command
# Field $10 (awk 1-indexed) = pane_command — what we match against.
# We only touch "pane" lines; "window" and "state" lines pass through unchanged.

set -euo pipefail

SAVE_FILE="${1:-}"

if [ -z "$SAVE_FILE" ] || [ ! -f "$SAVE_FILE" ]; then
    exit 0
fi

# Tabby utility process names that should never be restored by resurrect.
# These match #{pane_current_command} values for Tabby-managed panes.
# NOTE: macOS truncates process names to 15 chars (MAXCOMLEN), so
# "sidebar-renderer" (16 chars) appears as "sidebar-rendere" in save files.
# We use substring matching (index()) instead of exact match to handle this.
TABBY_PROCS="sidebar-renderer|sidebar-rendere|sidebar|tabby-daemon|pane-header"

# Use awk to filter: only drop lines where field 1 is "pane" AND field 10
# matches a Tabby process name (substring match for truncation safety).
# All other lines (window, state, etc.) pass through unchanged.
awk -F'\t' -v procs="$TABBY_PROCS" '
    BEGIN { n = split(procs, a, "|") }
    $1 == "pane" {
        drop = 0
        for (i = 1; i <= n; i++) {
            if ($10 == a[i] || index($10, a[i]) == 1) { drop = 1; break }
        }
        if (drop) next
    }
    { print }
' "$SAVE_FILE" > "${SAVE_FILE}.tabby_tmp" && mv "${SAVE_FILE}.tabby_tmp" "$SAVE_FILE"
