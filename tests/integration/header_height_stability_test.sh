#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: Header Height Stability Wiring ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
MAIN_GO="$PROJECT_ROOT/cmd/tabby-daemon/main.go"
PANE_HEADER_GO="$PROJECT_ROOT/cmd/pane-header/main.go"

if grep -q 'pane-border-status", "off"' "$MAIN_GO" && grep -q 'pane-border-lines", "off"' "$MAIN_GO"; then
  echo "✓ Header panes normalize border status/lines to off"
else
  echo "✗ Missing header border normalization (can cause header pane height growth)"
  exit 1
fi

if grep -q 'Header %s height=%d, forcing to %d' "$MAIN_GO"; then
  echo "✓ Header height correction logic present"
else
  echo "✗ Missing header height correction logic"
  exit 1
fi

if grep -q 'tea.NewProgram(model, tea.WithMouseCellMotion(), tea.WithReportFocus())' "$PANE_HEADER_GO" && ! grep -q 'WithAltScreen' "$PANE_HEADER_GO"; then
  echo "✓ Pane header renderer avoids alt-screen mode (prevents height growth loop)"
else
  echo "✗ Pane header renderer still uses alt-screen"
  exit 1
fi

echo "=== Header height stability wiring test passed ==="
