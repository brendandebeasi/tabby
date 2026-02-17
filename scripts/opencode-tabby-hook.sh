#!/usr/bin/env bash
set -eu

RAW_EVENT="${1:-}"
INDICATOR="/Users/b/git/tabby/scripts/set-tabby-indicator.sh"
LOG_FILE="/tmp/tabby-opencode-hook.log"

if [ -z "${TMUX_PANE:-}" ] && [ -n "${TMUX:-}" ]; then
    TMUX_PANE=$(tmux display-message -p '#{pane_id}' 2>/dev/null || true)
    export TMUX_PANE
fi

EVENT="$RAW_EVENT"
if [[ "$EVENT" =~ ^\{.*\}$ ]]; then
    PARSED=$(python3 - <<'PY' "$EVENT"
import json,sys
try:
    data=json.loads(sys.argv[1])
except Exception:
    print("")
    raise SystemExit
for key in ("event","type","name"):
    v=data.get(key)
    if isinstance(v,str) and v:
        print(v)
        break
PY
)
    if [ -n "$PARSED" ]; then
        EVENT="$PARSED"
    fi
fi

printf "%s event=%s tmux=%s pane=%s\n" "$(date '+%Y-%m-%d %H:%M:%S')" "$EVENT" "${TMUX:-}" "${TMUX_PANE:-}" >> "$LOG_FILE"

case "$EVENT" in
    start|busy|prompt|working|user_prompt_submit)
        "$INDICATOR" input 0
        "$INDICATOR" busy 1
        ;;
    complete|permission|question|subagent_complete|stop|done)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        ;;
    error|failed)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        ;;
    *)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        ;;
esac
