#!/usr/bin/env bash
set -e

echo "=== Integration Test: Marker Modal Wiring ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
COORDINATOR="$PROJECT_ROOT/cmd/tabby-daemon/coordinator.go"

if grep -q 'show_marker_results.sh' "$COORDINATOR"; then
    echo "✓ Marker search menu has tmux fallback command"
else
    echo "✗ Missing marker search fallback command wiring in coordinator"
    exit 1
fi

if grep -q 'markerSearchCommandPattern' "$COORDINATOR" && grep -q 'openMarkerPicker(clientID, scope, target, title)' "$COORDINATOR"; then
    echo "✓ Overlay menu selection upgrades to in-app marker picker"
else
    echo "✗ Missing in-app marker picker interception for menu selection"
    exit 1
fi

if grep -q "-Set Group Marker" "$COORDINATOR" && grep -q "-Set Marker" "$COORDINATOR"; then
    echo "✓ Window and group marker menus are present"
else
    echo "✗ Missing window/group marker menu entries"
    exit 1
fi

if grep -q 'MsgMarkerPicker' "$PROJECT_ROOT/pkg/daemon/protocol.go"; then
    echo "✓ Marker picker daemon protocol message is configured"
else
    echo "✗ Missing marker picker daemon protocol message"
    exit 1
fi

echo "=== Marker modal wiring test passed ==="
