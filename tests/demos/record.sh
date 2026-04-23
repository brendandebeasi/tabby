#!/usr/bin/env bash
# Record a tabby demo scenario via asciinema + agg.
#
# Usage:
#   tests/demos/record.sh <scenario-name>
#   tests/demos/record.sh 01-minimize            # records scenarios/01-minimize-driver.sh
#   tests/demos/record.sh all                    # every scenarios/*-driver.sh
#
# Outputs .cast and .gif into tests/demos/out/.
#
# Why this pipeline works where tmux-attach-inside-asciinema failed: the
# driver scripts never attach — they use `tmux capture-pane` from the outside
# and print the result to stdout. asciinema records that stdout stream, which
# agg can always render. See tests/demos/README.md for the full story.

set -euo pipefail

REPO="${TABBY_REPO:-/Users/b/git/tabby}"
OUT_DIR="$REPO/tests/demos/out"
SCEN_DIR="$REPO/tests/demos/scenarios"
mkdir -p "$OUT_DIR"

ASC_COLS="${ASC_COLS:-40}"
ASC_ROWS="${ASC_ROWS:-55}"
AGG_FONT_SIZE="${AGG_FONT_SIZE:-15}"
AGG_FONT_FAMILY="${AGG_FONT_FAMILY:-JetBrains Mono,Menlo,DejaVu Sans Mono}"
AGG_THEME="${AGG_THEME:-monokai}"
AGG_FPS="${AGG_FPS:-15}"

record_one() {
    local name="$1"
    local driver="$SCEN_DIR/${name}-driver.sh"
    if [ ! -x "$driver" ]; then
        echo "no driver found at $driver" >&2
        return 1
    fi
    local cast="$OUT_DIR/${name}.cast"
    local gif="$OUT_DIR/${name}.gif"
    rm -f "$cast" "$gif"

    echo "▶ recording $name ..."
    asciinema rec --quiet --overwrite --headless \
        --window-size "${ASC_COLS}x${ASC_ROWS}" \
        --idle-time-limit 2 \
        -c "bash $driver" \
        "$cast"

    echo "  → cast: $(du -h "$cast" | cut -f1)  ($cast)"

    if command -v agg >/dev/null 2>&1; then
        agg --theme "$AGG_THEME" \
            --font-size "$AGG_FONT_SIZE" \
            --font-family "$AGG_FONT_FAMILY" \
            --fps-cap "$AGG_FPS" \
            "$cast" "$gif" >/dev/null 2>&1
        echo "  → gif:  $(du -h "$gif" | cut -f1)  ($gif)"
    else
        echo "  (agg not installed; skipping gif)"
    fi
}

arg="${1:-all}"
if [ "$arg" = "all" ]; then
    shopt -s nullglob
    for drv in "$SCEN_DIR"/*-driver.sh; do
        name="$(basename "$drv" -driver.sh)"
        record_one "$name"
    done
else
    record_one "$arg"
fi
