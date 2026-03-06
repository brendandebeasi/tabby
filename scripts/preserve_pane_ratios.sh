#!/usr/bin/env bash
# Restore saved pane layout after a pane exits to preserve size ratios.
# Called from after-kill-pane hook. Without this, tmux equalizes pane sizes
# after a pane closes, which disrupts the user's layout.
#
# Usage: preserve_pane_ratios.sh [window_id]
# If window_id is passed, use it directly; otherwise query tmux.

WINDOW_ID="${1:-$(tmux display-message -p '#{window_id}' 2>/dev/null)}"
[ -z "$WINDOW_ID" ] && exit 0

# Daemon-managed system pane cleanup (header/sidebar) sets a one-shot skip flag
# so ratio restore doesn't corrupt mixed split layouts.
SKIP_ONCE=$(tmux show-option -gqv "@tabby_skip_preserve_${WINDOW_ID}" 2>/dev/null || true)
if [ "$SKIP_ONCE" = "1" ]; then
	tmux set-option -g "@tabby_skip_preserve_${WINDOW_ID}" "0" 2>/dev/null || true
	exit 0
fi

# Check if we have a saved layout for this window
SAVED_LAYOUT=$(tmux show-option -gqv "@tabby_layout_${WINDOW_ID}" 2>/dev/null)
[ -z "$SAVED_LAYOUT" ] && exit 0

# Only attempt restore if more than one pane remains
PANE_COUNT=$(tmux list-panes -t "$WINDOW_ID" 2>/dev/null | wc -l | tr -d ' ')
[ "$PANE_COUNT" -le 1 ] && exit 0

# NEW: Detect orphaned header panes before restoring layout
# If a header pane exists but its target pane is gone, skip restore
# to avoid creating a ghost-split state.
#
# Get all panes with their commands and window ID
PANES_OUTPUT=$(tmux list-panes -t "$WINDOW_ID" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' 2>/dev/null)

# Build set of content pane IDs (non-system panes)
declare -A CONTENT_PANES
while IFS='|' read -r pane_id cur_cmd start_cmd; do
	# Check if this is a system pane (header, sidebar, renderer, etc.)
	if [[ ! "$cur_cmd" =~ (pane-header|sidebar|renderer|tabbar|pane-bar|tabby-daemon) ]] && \
	   [[ ! "$start_cmd" =~ (pane-header|sidebar|renderer|tabbar|pane-bar|tabby-daemon) ]]; then
		CONTENT_PANES["$pane_id"]=1
	fi
done <<< "$PANES_OUTPUT"

# Check for orphaned headers
SKIP_RESTORE=0
while IFS='|' read -r pane_id cur_cmd start_cmd; do
	# Is this a header pane?
	if [[ "$cur_cmd" =~ pane-header ]] || [[ "$start_cmd" =~ pane-header ]]; then
		# Extract target pane from start command (format: pane-header -t %123)
		target_pane=$(echo "$start_cmd" | grep -oP '(?<=-t\s)\S+' || true)
		
		# If target pane doesn't exist in our content panes, skip restore
		if [ -n "$target_pane" ] && [ -z "${CONTENT_PANES[$target_pane]:-}" ]; then
			SKIP_RESTORE=1
			break
		fi
	fi
done <<< "$PANES_OUTPUT"

# If orphaned header detected, skip layout restore
if [ "$SKIP_RESTORE" = "1" ]; then
	exit 0
fi

# Apply the saved layout (may fail if pane count changed too much, that's fine).
# tmux select-layout intelligently redistributes space even with fewer panes.
tmux select-layout -t "$WINDOW_ID" "$SAVED_LAYOUT" 2>/dev/null || true

# Don't clear the saved layout immediately — it might be needed again when
# the orphaned header pane is cleaned up shortly after. The layout will be
# overwritten next time save_pane_layout.sh runs (on pane select, split, etc.).
exit 0
