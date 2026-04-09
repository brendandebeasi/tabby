#!/usr/bin/env bash
# resurrect_integration_test.sh
#
# Integration tests for Tabby + tmux-resurrect hook scripts.
# Tests save-file filtering and restore-hook behavior.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TABBY_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SAVE_HOOK="$TABBY_ROOT/bin/tabby-hook resurrect-save"
RESTORE_HOOK_BIN="$TABBY_ROOT/bin/tabby-hook"
FAILED=0

pass() { echo "✓ $1"; }
fail() { echo "✗ $1"; ((FAILED++)); }

echo "=== Integration Test: Resurrect Hooks ==="

# --- Test 1: Save hook strips Tabby pane lines ---

TMPFILE=$(mktemp)
printf 'window\tmain\t0\tcode\t1\t*\t{layout}\n' > "$TMPFILE"
printf 'pane\tmain\t0\tcode\t1\t*\t0\t/home\t1\tbash\tbash\n' >> "$TMPFILE"
printf 'pane\tmain\t0\tcode\t1\t*\t1\t/home\t0\tsidebar-renderer\tsidebar-renderer --session main\n' >> "$TMPFILE"
printf 'pane\tmain\t0\tcode\t1\t*\t2\t/home\t0\tpane-header\tpane-header --pane 0\n' >> "$TMPFILE"
printf 'pane\tmain\t1\tnotes\t0\t-\t0\t/home\t1\tvim\tvim notes.md\n' >> "$TMPFILE"
printf 'state\tsome_state_data\n' >> "$TMPFILE"

$SAVE_HOOK "$TMPFILE"

REMAINING_PANES=$(grep '^pane' "$TMPFILE" | wc -l | tr -d ' ')
if [ "$REMAINING_PANES" -eq 2 ]; then
    pass "Save hook kept 2 user panes (bash, vim)"
else
    fail "Save hook kept $REMAINING_PANES panes, expected 2"
fi

if grep -q 'sidebar-renderer\|pane-header' "$TMPFILE"; then
    fail "Save hook left Tabby pane lines in file"
else
    pass "Save hook stripped all Tabby pane lines"
fi

if grep -q '^window' "$TMPFILE" && grep -q '^state' "$TMPFILE"; then
    pass "Save hook preserved window and state lines"
else
    fail "Save hook damaged non-pane lines"
fi

rm -f "$TMPFILE"

# --- Test 1b: Save hook strips truncated process names (macOS MAXCOMLEN=15) ---

TMPFILE=$(mktemp)
printf 'pane\tmain\t0\tcode\t1\t*\t0\t/home\t1\tbash\tbash\n' > "$TMPFILE"
printf 'pane\tmain\t0\tcode\t1\t*\t1\t/home\t0\tsidebar-rendere\tsidebar-rendere\n' >> "$TMPFILE"
printf 'pane\tmain\t0\tcode\t1\t*\t2\t/home\t1\tvim\tvim\n' >> "$TMPFILE"

$SAVE_HOOK "$TMPFILE"

REMAINING_PANES=$(grep '^pane' "$TMPFILE" | wc -l | tr -d ' ')
if [ "$REMAINING_PANES" -eq 2 ]; then
    pass "Save hook strips truncated 'sidebar-rendere' (MAXCOMLEN)"
else
    fail "Save hook kept $REMAINING_PANES panes with truncated name, expected 2"
fi
rm -f "$TMPFILE"

# --- Test 2: Save hook handles missing/empty file gracefully ---

$SAVE_HOOK "" 2>/dev/null && pass "Save hook handles empty path" || fail "Save hook crashed on empty path"
$SAVE_HOOK "/nonexistent/file" 2>/dev/null && pass "Save hook handles missing file" || fail "Save hook crashed on missing file"

# --- Test 3: Save hook is idempotent (no Tabby panes = no change) ---

TMPFILE=$(mktemp)
printf 'pane\tmain\t0\tcode\t1\t*\t0\t/home\t1\tbash\tbash\n' > "$TMPFILE"
printf 'pane\tmain\t1\tnotes\t0\t-\t0\t/home\t1\tvim\tvim\n' >> "$TMPFILE"
BEFORE=$(cat "$TMPFILE")
$SAVE_HOOK "$TMPFILE"
AFTER=$(cat "$TMPFILE")

if [ "$BEFORE" = "$AFTER" ]; then
    pass "Save hook is idempotent on clean files"
else
    fail "Save hook modified a file with no Tabby panes"
fi
rm -f "$TMPFILE"

# --- Test 4: Restore hook binary exists and is executable ---

if [ -x "$RESTORE_HOOK_BIN" ]; then
    pass "Restore hook binary is executable"
else
    fail "Restore hook binary not found"
fi

# --- Test 5: Hook options are wired in tmux ---

SAVE_OPT=$(tmux show-option -gqv @resurrect-hook-post-save-layout 2>/dev/null || echo "")
RESTORE_OPT=$(tmux show-option -gqv @resurrect-hook-post-restore-all 2>/dev/null || echo "")

if echo "$SAVE_OPT" | grep -q "tabby-hook resurrect-save"; then
    pass "Save hook wired in tmux options"
else
    fail "Save hook not found in @resurrect-hook-post-save-layout (got: '$SAVE_OPT')"
fi

if echo "$RESTORE_OPT" | grep -q "tabby-hook resurrect-restore"; then
    pass "Restore hook wired in tmux options"
else
    fail "Restore hook not found in @resurrect-hook-post-restore-all (got: '$RESTORE_OPT')"
fi

# --- Results ---

echo ""
if [ "$FAILED" -eq 0 ]; then
    echo "=== All resurrect integration tests passed ==="
else
    echo "=== $FAILED resurrect test(s) FAILED ==="
    exit 1
fi
