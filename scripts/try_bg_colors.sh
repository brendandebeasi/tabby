#!/usr/bin/env bash

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Switching to background-colored tabs..."

tmux set-window-option -g window-status-format "#($CURRENT_DIR/../bin/render-tab-v2 normal #I '#W' '#{window_flags}')"
tmux set-window-option -g window-status-current-format "#($CURRENT_DIR/../bin/render-tab-v2 active #I '#W' '#{window_flags}')"

tmux refresh-client -S

echo "Now using background colors:"
echo "  SD|* = Red background"
echo "  GP|* = Dark gray background"
echo "  Default = Dark blue background"
echo ""
echo "To revert: ~/.tmux/plugins/tabby/scripts/fix_tab_colors.sh"
