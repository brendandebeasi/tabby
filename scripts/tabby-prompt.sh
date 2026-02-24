#!/usr/bin/env bash
# tabby-prompt.sh - Shell prompt integration for tabby
#
# Reads the current window's group color and icon from tmux and outputs
# them as shell variables. Source this from your shell config.
#
# Usage (zsh):
#
#   source ~/.tmux/plugins/tabby/scripts/tabby-prompt.sh
#
#   PROMPT='$(tabby_prompt_prefix)%~ %# '
#
# The function tabby_prompt_prefix outputs:  <icon> <colored-caret>
# Colors use zsh %F{#hex} syntax. Falls back to plain text outside tmux.
#
# Variables exported for advanced prompt customization:
#   TABBY_PROMPT_COLOR  - hex color of current window's group (e.g. #b4637a)
#   TABBY_PROMPT_ICON   - emoji/icon for current window's group (e.g. 🎬)

tabby_prompt_vars() {
    if [[ -z "$TMUX" ]]; then
        TABBY_PROMPT_COLOR=""
        TABBY_PROMPT_ICON=""
        return
    fi
    TABBY_PROMPT_COLOR=$(tmux display-message -p '#{@tabby_pane_active}' 2>/dev/null)
    TABBY_PROMPT_ICON=$(tmux display-message -p '#{@tabby_group_icon}' 2>/dev/null)
}

# tabby_prompt_prefix - outputs icon + colored prompt marker
# Designed to be called inside $PROMPT or precmd hooks.
tabby_prompt_prefix() {
    [[ -z "$TMUX" ]] && return

    local color icon
    color=$(tmux display-message -p '#{@tabby_pane_active}' 2>/dev/null)
    icon=$(tmux display-message -p '#{@tabby_group_icon}' 2>/dev/null)

    [[ -z "$color" ]] && color="#56949f"
    [[ -z "$icon" ]]  && icon="•"

    # zsh prompt expansion: %F{color}...%f for foreground color
    printf '%%F{%s}%s%%f ' "$color" "$icon"
}

# zsh precmd hook: refresh variables before each prompt draw.
# Avoids calling tmux twice if you use tabby_prompt_prefix in PROMPT.
_tabby_precmd() {
    tabby_prompt_vars
}

# Register the precmd hook (zsh only, no-op in bash)
if [[ -n "$ZSH_VERSION" ]]; then
    autoload -Uz add-zsh-hook 2>/dev/null
    add-zsh-hook precmd _tabby_precmd
fi
