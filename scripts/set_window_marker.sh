#!/usr/bin/env bash
set -eu

WINDOW_INDEX="${1:-}"
RAW_QUERY="${2:-}"
SESSION_TARGET="${3:-}"

if [ -z "$WINDOW_INDEX" ]; then
    exit 1
fi

QUERY="$(printf '%s' "$RAW_QUERY" | sed 's/^ *//; s/ *$//')"
if [ -z "$QUERY" ]; then
    exit 0
fi

MARKER=""

# If user typed a marker directly (including Unicode), use it as-is.
if printf '%s' "$QUERY" | LC_ALL=C grep -q '[^ -~]'; then
    MARKER="$QUERY"
else
    q="$(printf '%s' "$QUERY" | tr '[:upper:]' '[:lower:]')"
    case "$q" in
        *term*|*shell*|*console*) MARKER="💻" ;;
        *code*|*dev*|*program*) MARKER="🧠" ;;
        *folder*|*dir*|*file*) MARKER="📁" ;;
        *git*|*branch*|*repo*) MARKER="🌿" ;;
        *bug*|*fix*) MARKER="🐞" ;;
        *test*|*qa*) MARKER="🧪" ;;
        *db*|*data*|*sql*) MARKER="🗄️" ;;
        *web*|*world*|*globe*) MARKER="🌍" ;;
        *star*|*fav*) MARKER="★" ;;
        *heart*|*love*) MARKER="❤" ;;
        *fire*|*hot*) MARKER="🔥" ;;
        *rocket*|*launch*) MARKER="🚀" ;;
        *bolt*|*lightning*|*fast*) MARKER="⚡" ;;
        *cat*) MARKER="🐱" ;;
        *book*|*doc*) MARKER="📚" ;;
        *music*|*audio*) MARKER="🎵" ;;
        *) MARKER="$QUERY" ;;
    esac
fi

TMUX_TARGET=":$WINDOW_INDEX"
if [ -n "$SESSION_TARGET" ]; then
    TMUX_TARGET="${SESSION_TARGET}:$WINDOW_INDEX"
fi

tmux set-window-option -t "$TMUX_TARGET" @tabby_icon "$MARKER" >/dev/null 2>&1 || true
tmux display-message -d 1500 "Tabby marker -> $MARKER" 2>/dev/null || true
