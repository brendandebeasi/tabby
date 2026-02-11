#!/usr/bin/env bash
# Shared config path resolution for Tabby shell scripts.
# Source this file; it sets TABBY_CONFIG_FILE.
#
# Resolution order:
#   1. TABBY_CONFIG_DIR env var
#   2. ~/.config/tabby/config.yaml  (new canonical path)
#   3. ~/.tmux/plugins/tmux-tabs/config.yaml  (legacy)

if [ -n "$TABBY_CONFIG_DIR" ]; then
    TABBY_CONFIG_FILE="$TABBY_CONFIG_DIR/config.yaml"
elif [ -f "$HOME/.config/tabby/config.yaml" ]; then
    TABBY_CONFIG_FILE="$HOME/.config/tabby/config.yaml"
elif [ -f "$HOME/.tmux/plugins/tmux-tabs/config.yaml" ]; then
    echo "[tabby] deprecation: reading config from ~/.tmux/plugins/tmux-tabs/ -- move it to ~/.config/tabby/" >&2
    TABBY_CONFIG_FILE="$HOME/.tmux/plugins/tmux-tabs/config.yaml"
else
    # Default to new canonical path (will be created on install)
    TABBY_CONFIG_FILE="$HOME/.config/tabby/config.yaml"
fi
