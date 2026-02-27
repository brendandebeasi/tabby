#!/usr/bin/env bash
# Shared config path resolution for Tabby shell scripts.
# Source this file; it sets TABBY_CONFIG_FILE.
#
# Resolution: TABBY_CONFIG_DIR env > ~/.config/tabby/

if [ -n "${TABBY_CONFIG_DIR:-}" ]; then
    TABBY_CONFIG_FILE="$TABBY_CONFIG_DIR/config.yaml"
else
    TABBY_CONFIG_FILE="$HOME/.config/tabby/config.yaml"
fi
