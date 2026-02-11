#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=_config_path.sh
source "$SCRIPT_DIR/_config_path.sh"
CONFIG_FILE="$TABBY_CONFIG_FILE"
ORDER="${1:-index}"

if [ "$ORDER" = "index" ]; then
    sed -i.bak 's/sort_by: "group"/sort_by: "index"/' "$CONFIG_FILE"
    echo "Sidebar will now show windows in numerical order"
elif [ "$ORDER" = "group" ]; then
    sed -i.bak 's/sort_by: "index"/sort_by: "group"/' "$CONFIG_FILE"
    echo "Sidebar will now show windows grouped by project"
else
    echo "Usage: $0 [index|group]"
    exit 1
fi

pgrep -f "sidebar" | xargs kill -USR1 2>/dev/null || true
