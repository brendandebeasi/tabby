#!/usr/bin/env bash
set -u

DIRECTION="${1:-v}"
if [ "$DIRECTION" != "v" ] && [ "$DIRECTION" != "h" ]; then
  exit 1
fi

CURRENT_PANE="${TMUX_PANE:-}"
if [ -z "$CURRENT_PANE" ]; then
  CURRENT_PANE=$(tmux display-message -p '#{pane_id}' 2>/dev/null || echo "")
fi
[ -z "$CURRENT_PANE" ] && exit 0

PANE_META=$(tmux display-message -p -t "$CURRENT_PANE" '#{pane_current_command}|#{pane_start_command}|#{window_id}' 2>/dev/null || echo "")
CUR_CMD=${PANE_META%%|*}
REST=${PANE_META#*|}
START_CMD=${REST%%|*}
WINDOW_ID=${REST#*|}

TARGET_PANE="$CURRENT_PANE"

if printf '%s|%s' "$CUR_CMD" "$START_CMD" | grep -q 'pane-header'; then
  EXTRACTED=$(printf '%s' "$START_CMD" | sed -nE "s/.*-pane[[:space:]]+'([^']+)'.*/\1/p")
  if [ -z "$EXTRACTED" ]; then
    EXTRACTED=$(printf '%s' "$START_CMD" | sed -nE 's/.*-pane[[:space:]]+"([^"]+)".*/\1/p')
  fi
  if [ -z "$EXTRACTED" ]; then
    EXTRACTED=$(printf '%s' "$START_CMD" | sed -nE 's/.*-pane[[:space:]]+([^[:space:]]+).*/\1/p')
  fi
  if [ -n "$EXTRACTED" ]; then
    TARGET_PANE="$EXTRACTED"
  fi
fi

if ! tmux display-message -p -t "$TARGET_PANE" '#{pane_id}' >/dev/null 2>&1; then
  FALLBACK=""
  if [ -n "$WINDOW_ID" ]; then
    FALLBACK=$(tmux list-panes -t "$WINDOW_ID" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}|#{pane_active}' 2>/dev/null | awk -F'|' '$2 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $3 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $4 == "1" { print $1; exit }')
    if [ -z "$FALLBACK" ]; then
      FALLBACK=$(tmux list-panes -t "$WINDOW_ID" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' 2>/dev/null | awk -F'|' '$2 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ && $3 !~ /(sidebar|renderer|pane-header|tabbar|pane-bar|tabby-daemon)/ { print $1; exit }')
    fi
  fi
  [ -n "$FALLBACK" ] && TARGET_PANE="$FALLBACK"
fi

PANE_PATH=$(tmux display-message -p -t "$TARGET_PANE" '#{pane_current_path}' 2>/dev/null || echo "")
if [ -z "$PANE_PATH" ]; then
  PANE_PATH=$(tmux display-message -p '#{pane_current_path}' 2>/dev/null || echo "")
fi

TARGET_WIDTH=$(tmux display-message -p -t "$TARGET_PANE" '#{pane_width}' 2>/dev/null || echo "")
TARGET_HEIGHT=$(tmux display-message -p -t "$TARGET_PANE" '#{pane_height}' 2>/dev/null || echo "")

if [ "$DIRECTION" = "v" ]; then
  HALF=0
  if [ -n "$TARGET_HEIGHT" ] && [ "$TARGET_HEIGHT" -gt 0 ] 2>/dev/null; then
    HALF=$((TARGET_HEIGHT / 2))
  fi
  [ "$HALF" -lt 2 ] && HALF=2
  tmux split-window -v -t "$TARGET_PANE" -l "$HALF" -c "$PANE_PATH"
else
  HALF=0
  if [ -n "$TARGET_WIDTH" ] && [ "$TARGET_WIDTH" -gt 0 ] 2>/dev/null; then
    HALF=$((TARGET_WIDTH / 2))
  fi
  [ "$HALF" -lt 2 ] && HALF=2
  tmux split-window -h -t "$TARGET_PANE" -l "$HALF" -c "$PANE_PATH"
fi

exit 0
