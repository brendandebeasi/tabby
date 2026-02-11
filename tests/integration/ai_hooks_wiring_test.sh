#!/usr/bin/env bash
set -euo pipefail

echo "=== Integration Test: AI Hooks Wiring ==="

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd -P)"
SET_IND="$PROJECT_ROOT/scripts/set-tabby-indicator.sh"
OPENCODE_HOOK="$PROJECT_ROOT/scripts/opencode-tabby-hook.sh"
SETUP_HOOKS="$PROJECT_ROOT/scripts/setup-ai-hooks.sh"

if grep -q 'resolve_window_from_state' "$SET_IND" && grep -q 'state-recovery' "$SET_IND"; then
  echo "✓ Indicator script has state-recovery fallback"
else
  echo "✗ Indicator script missing state-recovery fallback"
  exit 1
fi

if grep -q 'busy 0' "$OPENCODE_HOOK" && grep -q 'input 1' "$OPENCODE_HOOK"; then
  echo "✓ OpenCode hook clears busy before input state"
else
  echo "✗ OpenCode hook missing busy->input transition"
  exit 1
fi

if grep -q '@mohak34/opencode-notifier@latest' "$SETUP_HOOKS" && grep -q 'Added opencode-notifier plugin' "$SETUP_HOOKS"; then
  echo "✓ Setup script auto-installs opencode notifier plugin"
else
  echo "✗ Setup script missing opencode plugin auto-install"
  exit 1
fi

echo "=== AI hooks wiring test passed ==="
