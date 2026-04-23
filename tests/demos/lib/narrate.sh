#!/usr/bin/env bash
# Narrated-driver helpers: print captions + inline sidebar snapshots so a
# recorder (vhs, asciinema, script) can capture the demo as a sequence of
# annotated frames. Renders a static "tabby stage" around the snapshot so the
# viewer sees window+sidebar chrome rather than raw ANSI output.

set -uo pipefail

# Colours for the narrator chrome
NARR_BG_RESET=$'\033[0m'
NARR_DIM=$'\033[2m'
NARR_BOLD=$'\033[1m'
NARR_CYAN=$'\033[38;2;86;147;159m'
NARR_ROSE=$'\033[38;2;180;99;122m'
NARR_IRIS=$'\033[38;2;144;122;169m'
NARR_FG=$'\033[38;2;75;72;82m'

# narr_caption <text>
# Clear screen, print a boxed caption, leave room below for the snapshot.
narr_caption() {
    local text="$1"
    printf '\n\n%s%s▸ %s%s\n' "$NARR_BOLD" "$NARR_CYAN" "$text" "$NARR_BG_RESET"
    printf '%s' "$NARR_FG"
    printf -- '─%.0s' {1..60}
    printf '%s\n' "$NARR_BG_RESET"
}

# narr_snapshot <pane_id>
# Capture a sidebar pane (ANSI preserved) and render it as a boxed frame so
# the viewer can tell it's the actual tabby output. Uses unicode box-drawing.
narr_snapshot() {
    local pane="$1"
    local capture
    capture="$(ss_tmux capture-pane -p -e -t "$pane")"
    # Trim trailing whitespace-only lines so the frame isn't 42 rows tall.
    capture="$(printf '%s' "$capture" | awk '
        { lines[NR] = $0 }
        END {
            last = NR
            while (last > 0) {
                line = lines[last]
                gsub(/\x1b\[[0-9;]*[A-Za-z]/, "", line)
                gsub(/[[:space:]]+$/, "", line)
                if (length(line) > 0) break
                last--
            }
            for (i = 1; i <= last; i++) print lines[i]
        }
    ')"
    # Box it
    local width=26
    printf '%s┌' "$NARR_FG"
    printf -- '─%.0s' $(seq 1 "$width")
    printf '┐%s\n' "$NARR_BG_RESET"
    while IFS= read -r line; do
        printf '%s│%s %s %s│%s\n' "$NARR_FG" "$NARR_BG_RESET" "$line" "$NARR_FG" "$NARR_BG_RESET"
    done <<< "$capture"
    printf '%s└' "$NARR_FG"
    printf -- '─%.0s' $(seq 1 "$width")
    printf '┘%s\n' "$NARR_BG_RESET"
}

# narr_pause <secs>  — sleep + show a faint "..." for recorder pacing
narr_pause() {
    sleep "${1:-1.5}"
}
