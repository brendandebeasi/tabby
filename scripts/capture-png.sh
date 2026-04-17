#!/usr/bin/env bash
# capture-png.sh - Capture full tmux session layout as HTML, then render to PNG
# Usage: capture-png.sh [session_id] [output.png]
# Output is saved to /tmp/tabby-capture.png on the Mac side

set -e

SESSION="${1:-}"
OUTPUT="${2:-/tmp/tabby-capture.png}"

# Get the target session (use most recently active one if not specified)
if [ -z "$SESSION" ]; then
    SESSION=$(tmux list-sessions -F "#{session_last_attached} #{session_id}" 2>/dev/null | sort -rn | head -1 | awk '{print $2}')
fi

if [ -z "$SESSION" ]; then
    echo "No tmux session found" >&2
    exit 1
fi

echo "Capturing session: $SESSION" >&2

# Get pane layout for this session
PANE_INFO=$(tmux list-panes -t "$SESSION" -a -F "#{window_index} #{pane_index} #{pane_left} #{pane_top} #{pane_width} #{pane_height} #{pane_id} #{pane_active} #{window_active}")

# Determine terminal dimensions (use first window for reference)
TERM_WIDTH=$(tmux display -t "$SESSION" -p "#{window_width}" 2>/dev/null || echo 188)
TERM_HEIGHT=$(tmux display -t "$SESSION" -p "#{window_height}" 2>/dev/null || echo 51)

# Font dimensions (monospace chars: ~8px wide, ~16px tall at standard size)
CHAR_W=8
CHAR_H=16
PADDING=10

IMG_W=$((TERM_WIDTH * CHAR_W + PADDING * 2))
IMG_H=$((TERM_HEIGHT * CHAR_H + PADDING * 2))

# Start HTML
HTML_FILE=$(mktemp /tmp/tabby-capture-XXXX.html)

cat > "$HTML_FILE" << 'HTMLHEAD'
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
    background: #000;
    font-family: 'Menlo', 'Monaco', 'Consolas', 'DejaVu Sans Mono', monospace;
    font-size: 13px;
    line-height: 16px;
}
.terminal {
    position: relative;
    background: #000;
}
.pane {
    position: absolute;
    overflow: hidden;
    white-space: pre;
}
.pane-content {
    white-space: pre;
    font-family: inherit;
    font-size: inherit;
    line-height: inherit;
}
.pane-border-right {
    position: absolute;
    right: 0;
    top: 0;
    bottom: 0;
    width: 1px;
    background: #444;
}
.pane-border-bottom {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 1px;
    background: #444;
}
</style>
</head>
<body>
HTMLHEAD

echo "<div class=\"terminal\" style=\"width:${IMG_W}px;height:${IMG_H}px;\">" >> "$HTML_FILE"

# Process each pane
while IFS=' ' read -r win_idx pane_idx pane_left pane_top pane_w pane_h pane_id is_active_pane is_active_win; do
    # Only capture panes from first window (active window)
    if [ "$is_active_win" != "1" ]; then
        continue
    fi

    # Pixel coordinates
    px_left=$(( pane_left * CHAR_W + PADDING ))
    px_top=$(( pane_top * CHAR_H + PADDING ))
    px_w=$(( pane_w * CHAR_W ))
    px_h=$(( pane_h * CHAR_H ))

    # Capture pane content with ANSI codes
    PANE_CONTENT=$(tmux capture-pane -t "$pane_id" -p -e -J 2>/dev/null || echo "")

    # Convert to HTML using aha (if available) or Python fallback
    if command -v aha >/dev/null 2>&1; then
        PANE_HTML=$(echo "$PANE_CONTENT" | aha --no-header 2>/dev/null || echo "<span>$PANE_CONTENT</span>")
    else
        # Fallback: escape HTML and wrap
        PANE_HTML=$(echo "$PANE_CONTENT" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g')
    fi

    cat >> "$HTML_FILE" << PANEHTML
<div class="pane" style="left:${px_left}px;top:${px_top}px;width:${px_w}px;height:${px_h}px;">
<div class="pane-content">${PANE_HTML}</div>
</div>
PANEHTML

done <<< "$PANE_INFO"

cat >> "$HTML_FILE" << 'HTMLFOOT'
</div>
</body>
</html>
HTMLFOOT

echo "HTML generated: $HTML_FILE" >&2
echo "$HTML_FILE"
