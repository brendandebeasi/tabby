#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: Marker Picker Visual QA ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"

if (cd "$PROJECT_ROOT" && go test ./cmd/sidebar-renderer -run 'TestRenderPickerModalShowsEmptyStateAndMeta|TestViewOverlaysPickerModal' -count=1 >/dev/null); then
  echo "✓ Picker modal visual snapshot tests pass"
else
  echo "✗ Picker modal visual snapshot tests failed"
  exit 1
fi

if (cd "$PROJECT_ROOT" && bash tests/e2e/capture_marker_picker.sh >/dev/null); then
  echo "✓ tmux screenshot capture for picker modal pass"
elif (cd "$PROJECT_ROOT" && bash tests/e2e/capture_marker_picker.sh >/dev/null); then
  echo "✓ tmux screenshot capture for picker modal pass (retry)"
else
  echo "✗ tmux screenshot capture for picker modal failed"
  exit 1
fi

BASE_MODAL="/tmp/sidebar-marker-picker-baseline-modal.txt"
CUR_MODAL="/tmp/sidebar-marker-picker-current-modal.txt"

awk '/Set Marker/{capture=1} capture{print} /Enter: apply/{if(capture){exit}}' "$PROJECT_ROOT/tests/screenshots/baseline/sidebar-marker-picker.txt" > "$BASE_MODAL"
awk '/Set Marker/{capture=1} capture{print} /Enter: apply/{if(capture){exit}}' "$PROJECT_ROOT/tests/screenshots/current/sidebar-marker-picker.txt" > "$CUR_MODAL"

if [ ! -s "$BASE_MODAL" ] || [ ! -s "$CUR_MODAL" ]; then
  echo "✗ failed to extract modal region from screenshot artifacts"
  exit 1
fi

if diff -u "$BASE_MODAL" "$CUR_MODAL" >/tmp/sidebar-marker-picker.diff 2>&1; then
  echo "✓ marker picker modal region matches baseline"
else
  echo "✗ marker picker modal region differs from baseline (see /tmp/sidebar-marker-picker.diff)"
  exit 1
fi

echo "=== Marker picker visual QA passed ==="
