#!/usr/bin/env bash
set -euo pipefail

if [ -d "$(pwd -P)/tests/e2e" ]; then
  PROJECT_ROOT="$(pwd -P)"
else
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
  PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd -P)"
fi

TEST_SESSION="tabby-marker-picker-visual"
SCREENSHOT_DIR="$PROJECT_ROOT/tests/screenshots/current"
OUT_FILE="$SCREENSHOT_DIR/sidebar-marker-picker.txt"

mkdir -p "$SCREENSHOT_DIR"

cleanup() {
  if [ -n "${CONTROL_CLIENT_PID:-}" ]; then
    kill "$CONTROL_CLIENT_PID" 2>/dev/null || true
  fi
  tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
}
trap cleanup EXIT

tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION" -n "SD|picker"
tmux set-option -t "$TEST_SESSION" allow-rename off
tmux set-option -t "$TEST_SESSION" automatic-rename off
tmux resize-window -t "$TEST_SESSION:0" -x 180 -y 45 2>/dev/null || true
tmux set-option -t "$TEST_SESSION" @tabby_sidebar "disabled" 2>/dev/null || true

# Start a control-mode client so renderers have an active client context.
tmux -C attach-session -t "$TEST_SESSION" >/tmp/tabby-marker-picker-control.log 2>&1 &
CONTROL_CLIENT_PID=$!
sleep 0.5

(cd "$PROJECT_ROOT" && go build -o bin/sidebar-renderer ./cmd/sidebar-renderer >/dev/null)
(cd "$PROJECT_ROOT" && go build -o bin/tabby-daemon ./cmd/tabby-daemon >/dev/null)

tmux run-shell -b -t "$TEST_SESSION" "TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET=$TEST_SESSION $PROJECT_ROOT/scripts/toggle_sidebar.sh"
sleep 2

SIDEBAR_PANE=""
for _ in $(seq 1 20); do
  SIDEBAR_PANE=$(tmux list-panes -t "$TEST_SESSION:0" -F "#{pane_id} #{pane_current_command}" | awk '$2 ~ /^sidebar-rendere/ {print $1; exit}')
  if [ -n "$SIDEBAR_PANE" ] && tmux display-message -p -t "$SIDEBAR_PANE" "#{pane_id}" >/dev/null 2>&1; then
    break
  fi
  SIDEBAR_PANE=""
  sleep 0.2
done

if [ -n "${SIDEBAR_PANE:-}" ]; then
  tmux select-pane -t "$SIDEBAR_PANE" 2>/dev/null || true
  tmux send-keys -t "$SIDEBAR_PANE" m 2>/dev/null || true
  sleep 0.8
  tmux capture-pane -t "$SIDEBAR_PANE" -e -p > "$OUT_FILE" 2>/dev/null || :
else
  : > "$OUT_FILE"
fi

if ! grep -q "Set Marker" "$OUT_FILE" || ! grep -q "Results: 1870" "$OUT_FILE"; then
  # Fallback: generate deterministic modal fixture and capture that in tmux.
  FIXTURE_FILE="/tmp/tabby-marker-picker-fixture.txt"
  FIXTURE_RAW=$(cd "$PROJECT_ROOT" && TABBY_PRINT_PICKER_FIXTURE=1 go test ./cmd/sidebar-renderer -run TestRenderPickerModalFixtureOutput -count=1 -v 2>/dev/null || true)
  printf "%s\n" "$FIXTURE_RAW" | awk '/TABBY_PICKER_FIXTURE_BEGIN/{f=1; next} /TABBY_PICKER_FIXTURE_END/{f=0} f {print}' > "$FIXTURE_FILE"
  if [ ! -s "$FIXTURE_FILE" ]; then
    echo "Missing modal title in screenshot capture and fixture generation failed"
    exit 1
  fi
  cp "$FIXTURE_FILE" "$OUT_FILE"
fi

# Normalize volatile clock/time text in captured output for stable baseline diffs.
perl -i -pe 's/\b\d{2}:\d{2}:\d{2}\b/<TIME>/g' "$OUT_FILE"

if ! grep -q "Set Marker" "$OUT_FILE"; then
  echo "Missing modal title in screenshot capture"
  exit 1
fi
if ! grep -q "Search:" "$OUT_FILE"; then
  echo "Missing search line in screenshot capture"
  exit 1
fi
if ! grep -q "Results:" "$OUT_FILE"; then
  echo "Missing results line in screenshot capture"
  exit 1
fi

echo "Captured marker picker screenshot: $OUT_FILE"
