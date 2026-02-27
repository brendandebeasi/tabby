#!/usr/bin/env bash
# HSL color picker for tabby — converts H/S/L to hex and applies it.
# Usage: hsl_color_picker.sh <mode> <target> <H> <S> <L>
#   mode:   "window" or "group"
#   target: window target (e.g. ":%d" or ":@123") or group name
#   H:      hue 0-360
#   S:      saturation 0-100
#   L:      lightness 0-100
set -eu

MODE="${1:-}"
TARGET="${2:-}"
H="${3:-0}"
S="${4:-100}"
L="${5:-50}"

# Strip any trailing % from S and L (user might type "50%")
S="${S%%%}"
L="${L%%%}"

# Clamp values
H=$(( H < 0 ? 0 : (H > 360 ? 360 : H) ))
S=$(( S < 0 ? 0 : (S > 100 ? 100 : S) ))
L=$(( L < 0 ? 0 : (L > 100 ? 100 : L) ))

# HSL to RGB conversion using awk (pure POSIX, no python needed)
HEX=$(awk -v h="$H" -v s="$S" -v l="$L" 'BEGIN {
    s = s / 100.0
    l = l / 100.0
    c = (1 - (2*l - 1 < 0 ? -(2*l - 1) : 2*l - 1)) * s
    x = c * (1 - ((h/60) % 2 - 1 < 0 ? -((h/60) % 2 - 1) : (h/60) % 2 - 1))
    m = l - c/2
    if      (h < 60)  { r=c; g=x; b=0 }
    else if (h < 120) { r=x; g=c; b=0 }
    else if (h < 180) { r=0; g=c; b=x }
    else if (h < 240) { r=0; g=x; b=c }
    else if (h < 300) { r=x; g=0; b=c }
    else              { r=c; g=0; b=x }
    R = int((r+m)*255 + 0.5)
    G = int((g+m)*255 + 0.5)
    B = int((b+m)*255 + 0.5)
    printf "#%02x%02x%02x\n", R, G, B
}')

if [ -z "$HEX" ]; then
    exit 1
fi

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

if [ "$MODE" = "window" ]; then
    tmux set-window-option -t "$TARGET" @tabby_color "$HEX"
elif [ "$MODE" = "group" ]; then
    COLOR_SCRIPT="$CURRENT_DIR/scripts/set_group_color.sh"
    if [ -x "$COLOR_SCRIPT" ]; then
        "$COLOR_SCRIPT" "$TARGET" "$HEX"
    fi
fi
