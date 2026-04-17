#!/usr/bin/env bash
# Captures current tmux session to a text file with escape codes
# Usage: capture-screen.sh [output_file]

OUTPUT="${1:-/tmp/tabby-screen-capture.txt}"
SESSION="${2:-}"

if [ -n "$SESSION" ]; then
    TARGET="-t $SESSION"
else
    TARGET=""
fi

# Capture all panes with ANSI escape codes
{
    echo "=== tmux capture $(date) ==="
    echo "=== session: $(tmux display-message -p '#{session_id} #{session_name}') ==="
    echo "=== windows: ==="
    tmux list-windows $TARGET -F "  #{window_index}: #{window_name} [#{window_width}x#{window_height}] #{?window_active,(active),}"
    echo ""

    # Capture each pane
    tmux list-panes $TARGET -a -F "#{window_index}.#{pane_index} #{pane_current_command} #{pane_width}x#{pane_height}" | while read info; do
        WIN=$(echo $info | cut -d. -f1)
        PANE=$(echo $info | cut -d' ' -f1)
        echo "=== pane $PANE ==="
        tmux capture-pane -p -e -t "$PANE" 2>/dev/null | head -60
        echo ""
    done
} > "$OUTPUT" 2>&1

echo "Captured to $OUTPUT"
