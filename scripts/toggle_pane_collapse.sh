#!/usr/bin/env bash
set -euo pipefail

TARGET=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--target)
      TARGET="${2:-}"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$TARGET" ]]; then
  TARGET="${TMUX_PANE:-}"
fi
[[ -z "$TARGET" ]] && exit 0

collapsed=$(tmux show-options -pqv -t "$TARGET" @tabby_pane_collapsed 2>/dev/null || echo "0")
if [[ "$collapsed" == "1" ]]; then
  prev_h=$(tmux show-options -pqv -t "$TARGET" @tabby_pane_prev_height 2>/dev/null || echo "15")
  [[ -z "$prev_h" ]] && prev_h="15"
  if ! [[ "$prev_h" =~ ^[0-9]+$ ]]; then
    prev_h="15"
  fi
  tmux resize-pane -t "$TARGET" -y "$prev_h" 2>/dev/null || true
  tmux set-option -pq -t "$TARGET" @tabby_pane_collapsed "0" 2>/dev/null || true
else
  cur_h=$(tmux display-message -p -t "$TARGET" '#{pane_height}' 2>/dev/null || echo "0")
  if [[ "$cur_h" =~ ^[0-9]+$ ]] && [[ "$cur_h" -gt 1 ]]; then
    tmux set-option -pq -t "$TARGET" @tabby_pane_prev_height "$cur_h" 2>/dev/null || true
  fi
  tmux resize-pane -t "$TARGET" -y 1 2>/dev/null || true
  tmux set-option -pq -t "$TARGET" @tabby_pane_collapsed "1" 2>/dev/null || true
fi

exit 0
