#!/usr/bin/env bash
# tabby-prompt.sh - Shell prompt integration for tabby
#
# Reads the current window's group color and icon from tmux window options
# set by tabby-daemon, and renders a colored badge in the shell prompt.
#
# SETUP (zsh - add to ~/.zshrc):
#
#   source ~/.tmux/plugins/tmux-tabs/scripts/tabby-prompt.sh
#   PROMPT='$(tabby_prompt_prefix)%~ %# '
#
# CONFIGURATION (set before sourcing, or export in ~/.zshrc):
#
#   TABBY_PROMPT_STYLE   - rendering style (default: badge)
#                          badge     : colored bg box with icon  " icon "
#                          fg_only   : icon in group color, no bg
#                          icon_only : plain icon, no color
#                          off       : disable (tabby_prompt_prefix outputs nothing)
#
#   TABBY_PROMPT_FALLBACK_ICON  - icon when @tabby_prompt_icon is unset (default: •)
#
# TMUX OPTIONS READ (set by tabby-daemon when prompt.shell_integration: true):
#   @tabby_pane_active   - resolved hex bg color for the window
#   @tabby_prompt_icon   - effective icon (window-specific > group default > fallback)

# _tabby_hex_to_rgb <#rrggbb> <var_r> <var_g> <var_b>
_tabby_hex_to_rgb() {
    local hex="${1#\#}"
    printf -v "$2" '%d' "0x${hex:0:2}"
    printf -v "$3" '%d' "0x${hex:2:2}"
    printf -v "$4" '%d' "0x${hex:4:2}"
}

tabby_prompt_prefix() {
    local style="${TABBY_PROMPT_STYLE:-badge}"
    [[ "$style" == "off" || -z "$TMUX" ]] && return

    local fallback="${TABBY_PROMPT_FALLBACK_ICON:-•}"

    # Single tmux call for both values
    local raw
    raw=$(tmux display-message -p '#{@tabby_pane_active}|#{@tabby_prompt_icon}' 2>/dev/null)
    local color="${raw%|*}"
    local icon="${raw#*|}"

    [[ -z "$color" || "$color" != \#* ]] && color="#56949f"
    [[ -z "$icon" ]] && icon="$fallback"

    case "$style" in
        fg_only)
            local r g b
            _tabby_hex_to_rgb "$color" r g b
            # Colored icon text, no background
            printf '%%{\e[38;2;%d;%d;%dm%%}%s%%{\e[0m%%} ' "$r" "$g" "$b" "$icon"
            ;;
        icon_only)
            printf '%s ' "$icon"
            ;;
        badge|*)
            local r g b
            _tabby_hex_to_rgb "$color" r g b
            # Auto-contrast fg: luminance decides white vs dark text
            local lum=$(( (299 * r + 587 * g + 114 * b) / 1000 ))
            local fr fg fb
            if (( lum > 140 )); then
                fr=30; fg=30; fb=30
            else
                fr=255; fg=255; fb=255
            fi
            # Colored bg badge: %{...%} marks zero-width ANSI for zsh line length
            printf '%%{\e[38;2;%d;%d;%dm\e[48;2;%d;%d;%dm%%} %s %%{\e[0m%%} ' \
                "$fr" "$fg" "$fb" "$r" "$g" "$b" "$icon"
            ;;
    esac
}

if [[ -n "$ZSH_VERSION" ]]; then
    autoload -Uz add-zsh-hook 2>/dev/null
    # No precmd needed; tabby_prompt_prefix queries tmux inline each render
    setopt PROMPT_SUBST
fi
