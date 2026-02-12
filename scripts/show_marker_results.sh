#!/usr/bin/env bash
set -eu

SCOPE="${1:-}"      # window | group
TARGET="${2:-}"     # window index or group name
RAW_QUERY="${3:-}"
SESSION="${4:-}"

if [ -z "$SCOPE" ] || [ -z "$TARGET" ]; then
    exit 1
fi

QUERY="$(printf '%s' "$RAW_QUERY" | sed 's/^ *//; s/ *$//')"
if [ -z "$QUERY" ]; then
    exit 0
fi
QLOW="$(printf '%s' "$QUERY" | tr '[:upper:]' '[:lower:]')"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CATALOG=$(cat <<'EOF'
🚀|Rocket|rocket launch ship
⚡|Bolt|bolt lightning fast
🔥|Fire|fire hot burn
💻|Terminal|term shell console
🧠|Code|code dev program
📁|Folder|folder dir file
🌿|Git|git branch repo
🐞|Bug|bug fix issue
🧪|Test|test qa check
🗄️|Database|db data sql
🌍|Globe|web world globe
★|Star|star fav favorite
❤|Heart|heart love
🐱|Cat|cat
📚|Books|book docs
🎵|Music|music audio
EOF
)

matches=0
max_matches=12
menu=(tmux display-menu -O -T "Search Results: $QUERY")

while IFS='|' read -r marker name keywords; do
    line="$marker $name $keywords"
    line_low="$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')"
    if printf '%s' "$line_low" | grep -Fq "$QLOW"; then
        if [ "$SCOPE" = "window" ]; then
            target_ref=":$TARGET"
            if [ -n "$SESSION" ]; then
                target_ref="$SESSION:$TARGET"
            fi
            action=$(printf "set-window-option -t %q @tabby_icon %q" "$target_ref" "$marker")
        else
            group_inner="$SCRIPT_DIR/set_group_marker.sh \"$TARGET\" \"$marker\""
            action=$(printf "run-shell %q" "$group_inner")
        fi
        menu+=("  $marker $name" "" "$action")
        matches=$((matches + 1))
        if [ "$matches" -ge "$max_matches" ]; then
            break
        fi
    fi
done <<< "$CATALOG"

# Always allow direct input as fallback.
if [ "$SCOPE" = "window" ]; then
    target_ref=":$TARGET"
    if [ -n "$SESSION" ]; then
        target_ref="$SESSION:$TARGET"
    fi
    win_inner="$SCRIPT_DIR/set_window_marker.sh $TARGET \"$QUERY\" $SESSION"
    direct_action=$(printf "run-shell %q" "$win_inner")
    clear_action=$(printf "set-window-option -t %q -u @tabby_icon" "$target_ref")
else
    group_direct_inner="$SCRIPT_DIR/set_group_marker.sh \"$TARGET\" \"$QUERY\""
    direct_action=$(printf "run-shell %q" "$group_direct_inner")
    group_clear_inner="$SCRIPT_DIR/set_group_marker.sh \"$TARGET\" \"\""
    clear_action=$(printf "run-shell %q" "$group_clear_inner")
fi

if [ "$matches" -eq 0 ]; then
    menu+=("  Use '$QUERY'" "" "$direct_action")
else
    menu+=("" "" "")
    menu+=("  Use '$QUERY'" "" "$direct_action")
fi
menu+=("  Clear Marker" "" "$clear_action")

"${menu[@]}"
