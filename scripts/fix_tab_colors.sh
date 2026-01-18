#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Fixing tab colors..."

tmux set-option -g status on

for i in 0 1 2 3 4 5 6 7 8 9 10; do
    tmux set-option -gu "status-format[$i]" 2>/dev/null || true
done

tmux set-window-option -g window-status-style "none"
tmux set-window-option -g window-status-current-style "none"

tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab normal #I '#W' '#{window_flags}')"
tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab active #I '#W' '#{window_flags}')"

tmux set-window-option -g window-status-separator ""

tmux refresh-client -S

echo "Tab colors fixed. Each group should maintain its color:"
echo "  SD|* windows = red/pink"
echo "  GP|* windows = gray with 🔫"
echo "  Other windows = light gray"
