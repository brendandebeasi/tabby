#!/usr/bin/env bash
set -e

echo "=== Integration Test: Context Menu Behavior Guards ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
SIDEBAR_RENDERER="$PROJECT_ROOT/cmd/sidebar-renderer/main.go"

if grep -q 'case tea.BlurMsg:' "$SIDEBAR_RENDERER" && grep -q 'm.menuDismiss()' "$SIDEBAR_RENDERER"; then
    echo "✓ Sidebar menu is dismissed on blur"
else
    echo "✗ Missing blur-based menu dismissal guard in sidebar renderer"
    exit 1
fi

if grep -q 'tea.WithReportFocus()' "$SIDEBAR_RENDERER"; then
    echo "✓ Sidebar renderer enables focus reporting"
else
    echo "✗ Missing tea.WithReportFocus in sidebar renderer program setup"
    exit 1
fi

echo "=== Context menu behavior guard test passed ==="
