#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: Daemon Health Check Reconnect Wiring ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
TOGGLE="$PROJECT_ROOT/scripts/toggle_sidebar_daemon.sh"
RESTORE="$PROJECT_ROOT/scripts/restore_sidebar.sh"

if grep -q 'stat -f %z' "$TOGGLE" && grep -q 'for _ in $(seq 1 10); do' "$TOGGLE"; then
  echo "✓ Toggle health check uses file-size probe with retry window"
else
  echo "✗ Toggle health check missing file-size probe/retry"
  exit 1
fi

if grep -q 'stat -f %z' "$RESTORE" && grep -q 'for _ in $(seq 1 10); do' "$RESTORE"; then
  echo "✓ Restore health check uses file-size probe with retry window"
else
  echo "✗ Restore health check missing file-size probe/retry"
  exit 1
fi

if grep -q 'stat -f %m' "$TOGGLE" || grep -q 'stat -f %m' "$RESTORE"; then
  echo "✗ Second-granularity mtime probe still present in reconnect health checks"
  exit 1
fi

echo "=== Daemon health check reconnect wiring test passed ==="
