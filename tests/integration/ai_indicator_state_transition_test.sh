#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: AI Indicator State Transitions ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
SET_IND="$PROJECT_ROOT/scripts/set-tabby-indicator.sh"

WIN_IDX="$(tmux display-message -p '#{window_index}')"
PANE_ID="$(tmux display-message -p '#{pane_id}')"

cleanup() {
  tmux set-option -w -t ":$WIN_IDX" -u @tabby_busy 2>/dev/null || true
  tmux set-option -w -t ":$WIN_IDX" -u @tabby_input 2>/dev/null || true
  tmux set-option -w -t ":$WIN_IDX" -u @tabby_bell 2>/dev/null || true
}
trap cleanup EXIT

cleanup

TMUX_PANE="$PANE_ID" "$SET_IND" busy 1
BUSY_VAL="$(tmux show-window-options -t ":$WIN_IDX" -v @tabby_busy 2>/dev/null || true)"
if [ "$BUSY_VAL" != "1" ]; then
  echo "✗ busy 1 did not set @tabby_busy on window $WIN_IDX"
  exit 1
fi

TMUX_PANE="$PANE_ID" "$SET_IND" busy 0
BUSY_CLEARED="$(tmux show-window-options -t ":$WIN_IDX" -v @tabby_busy 2>/dev/null || true)"
if [ -n "$BUSY_CLEARED" ]; then
  echo "✗ busy 0 did not clear @tabby_busy on window $WIN_IDX"
  exit 1
fi

TMUX_PANE="$PANE_ID" "$SET_IND" input 1
INPUT_VAL="$(tmux show-window-options -t ":$WIN_IDX" -v @tabby_input 2>/dev/null || true)"
if [ "$INPUT_VAL" != "1" ]; then
  echo "✗ input 1 did not set @tabby_input on window $WIN_IDX"
  exit 1
fi

TMUX_PANE="$PANE_ID" "$SET_IND" input 0
INPUT_CLEARED="$(tmux show-window-options -t ":$WIN_IDX" -v @tabby_input 2>/dev/null || true)"
if [ -n "$INPUT_CLEARED" ]; then
  echo "✗ input 0 did not clear @tabby_input on window $WIN_IDX"
  exit 1
fi

TMUX_PANE="$PANE_ID" "$SET_IND" bell 1
BELL_VAL="$(tmux show-window-options -t ":$WIN_IDX" -v @tabby_bell 2>/dev/null || true)"
if [ "$BELL_VAL" != "1" ]; then
  echo "✗ bell 1 did not set @tabby_bell on window $WIN_IDX"
  exit 1
fi

echo "✓ busy/input/bell transitions apply correctly on current window"
echo "=== AI indicator state transition test passed ==="
