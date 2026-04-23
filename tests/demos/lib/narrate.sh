#!/usr/bin/env bash
# Narrated-driver helpers: print captions + inline sidebar snapshots so a
# recorder (asciinema → agg) can capture the demo as a sequence of annotated
# frames.

set -uo pipefail

# Plain-foreground colour codes. No backgrounds — the sidebar has its own
# group-coloured backgrounds and any chrome we add would clash with them.
_NC='\033[0m'          # reset
_BOLD='\033[1m'
_CYAN='\033[38;2;86;147;159m'
_MUTED='\033[38;2;121;117;147m'

# narr_caption <text>
# Print a short header announcing the next scene. Always resets colour state
# at the end so the sidebar snapshot below starts from a clean baseline.
narr_caption() {
    local text="$1"
    printf '\n%b▸ %s%b\n' "${_BOLD}${_CYAN}" "$text" "$_NC"
    printf '%b' "$_MUTED"
    printf -- '─%.0s' {1..40}
    printf '%b\n' "$_NC"
}

# narr_snapshot <pane_id>
# Capture a sidebar pane (ANSI preserved) and print it verbatim. No wrapping
# frame — the sidebar already has its own group-coloured backgrounds, and
# anything we overlay bleeds into them. Trailing blank rows are trimmed.
narr_snapshot() {
    local pane="$1"
    # Each line ends with a hard reset so the sidebar's own background colour
    # can't leak onto the next caption / blank row after the snapshot ends.
    ss_tmux capture-pane -p -e -t "$pane" | awk '
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
            for (i = 1; i <= last; i++) printf "%s\033[0m\n", lines[i]
        }
    '
}

# narr_pause <secs>
narr_pause() { sleep "${1:-1.5}"; }
