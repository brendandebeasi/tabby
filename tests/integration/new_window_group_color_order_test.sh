#!/usr/bin/env bash
set -euo pipefail

# Integration test for new window behavior:
# 1. Created in the same group the user is in
# 2. Created with the same color (unless dir has another color)
# 3. Appears below the user's current window in the tab bar

PROJECT_ROOT="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
TABBY_TEST_SOCKET="tabby-tests-new-win-$$-$(date +%s)"
TABBY_TMUX_REAL="$(command -v tmux)"
TABBY_TMUX_WRAPPER_DIR="$(mktemp -d /tmp/tabby-tests-new-win-tmux.XXXXXX)"
cat > "$TABBY_TMUX_WRAPPER_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$TABBY_TMUX_REAL" -L "$TABBY_TEST_SOCKET" -f /dev/null "\$@"
EOF
chmod +x "$TABBY_TMUX_WRAPPER_DIR/tmux"
export PATH="$TABBY_TMUX_WRAPPER_DIR:$PATH"

TEST_SESSION="tabby-test-new-win-behavior-$$"

PASS=0
FAIL=0

pass() { echo "✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "✗ $1" >&2; FAIL=$((FAIL + 1)); }

cleanup() {
  tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true
  rm -rf "$TABBY_TMUX_WRAPPER_DIR" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Integration Test: New Window Group, Color & Order ==="

tmux kill-server 2>/dev/null || true
tmux new-session -d -s "$TEST_SESSION" -n "win-1"
SESSION_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{session_id}')"
W1_ID="$(tmux display-message -p -t "$TEST_SESSION:win-1" '#{window_id}')"

# Set window 1 group to "ProjectAlpha" and custom color to "#112233"
tmux set-window-option -t "$W1_ID" @tabby_group "ProjectAlpha"
tmux set-window-option -t "$W1_ID" @tabby_color "#112233"

# ─── Test 1: Inherit group and color from current window ───
echo ""
echo "--- Test 1: Inherit group and color from current window ---"

# Firing window is W1
"$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" -after "$W1_ID" 2>/dev/null || true

# Find the window created after W1 (should be at index 1)
W2_ID="$(tmux display-message -p -t "$TEST_SESSION:1" '#{window_id}')"
W2_GROUP="$(tmux show-window-options -t "$W2_ID" -v @tabby_group 2>/dev/null || echo "")"
W2_COLOR="$(tmux show-window-options -t "$W2_ID" -v @tabby_color 2>/dev/null || echo "")"

if [ "$W2_GROUP" = "ProjectAlpha" ]; then
  pass "New window created in same group as user's current window ('ProjectAlpha')"
else
  fail "New window group is '$W2_GROUP', expected 'ProjectAlpha'"
fi

if [ "$W2_COLOR" = "#112233" ]; then
  pass "New window inherited same color as current window ('#112233')"
else
  fail "New window color is '$W2_COLOR', expected '#112233'"
fi

# ─── Test 2: Appears below current window when user is between windows ───
echo ""
echo "--- Test 2: Appears below current window in the tab bar ---"

# Currently we have W1 (idx 0), W2 (idx 1). Let's create W3 at the end.
tmux new-window -t "$SESSION_ID:" -n "win-3"
W3_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')"
tmux set-window-option -t "$W3_ID" @tabby_group "ProjectAlpha"

# Now window list in session:
# Index 0: W1
# Index 1: W2
# Index 2: W3
# Now user focuses W1 (Index 0).
tmux select-window -t "$W1_ID"
CURRENT_WIN="$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')"

# User creates new window while focused on W1
"$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true

# The new window should appear directly below W1 (so at Index 1).
# W2 should have moved to Index 2, and W3 to Index 3.
NEW_WIN_AT_1="$(tmux display-message -p -t "$TEST_SESSION:1" '#{window_id}')"
WIN_AT_2="$(tmux display-message -p -t "$TEST_SESSION:2" '#{window_id}')"
WIN_AT_3="$(tmux display-message -p -t "$TEST_SESSION:3" '#{window_id}')"

if [ "$NEW_WIN_AT_1" != "$W1_ID" ] && [ "$NEW_WIN_AT_1" != "$W2_ID" ] && [ "$NEW_WIN_AT_1" != "$W3_ID" ]; then
  pass "New window inserted immediately below current window W1 (at index 1)"
else
  fail "New window not at index 1 (got $NEW_WIN_AT_1)"
fi

if [ "$WIN_AT_2" = "$W2_ID" ] && [ "$WIN_AT_3" = "$W3_ID" ]; then
  pass "Subsequent windows W2 and W3 shifted down (to indices 2 and 3)"
else
  fail "Subsequent windows did not shift properly: idx 2 is $WIN_AT_2, idx 3 is $WIN_AT_3"
fi

# ─── Test 3: Directory color overrides current window color ───
echo ""
echo "--- Test 3: Directory color overrides current window color ---"

# Set up a mock cwd-colors.json
TEST_STATE_DIR="$(mktemp -d)"
export TABBY_STATE_DIR="$TEST_STATE_DIR"
mkdir -p "$TEST_STATE_DIR"

SPECIAL_DIR="/tmp/test-special-dir-$$"
mkdir -p "$SPECIAL_DIR"

cat <<EOF > "$TEST_STATE_DIR/cwd-colors.json"
{
  "$SPECIAL_DIR": {
    "color": "#e74c3c"
  }
}
EOF

# Current window W1 has group ProjectAlpha and color #112233.
# Open new window in SPECIAL_DIR (which has color #e74c3c).
"$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" -path "$SPECIAL_DIR" -after "$W1_ID" 2>/dev/null || true

DIR_WIN_ID="$(tmux display-message -p -t "$TEST_SESSION:1" '#{window_id}')"
DIR_WIN_GROUP="$(tmux show-window-options -t "$DIR_WIN_ID" -v @tabby_group 2>/dev/null || echo "")"
DIR_WIN_COLOR="$(tmux show-window-options -t "$DIR_WIN_ID" -v @tabby_color 2>/dev/null || echo "")"

if [ "$DIR_WIN_GROUP" = "ProjectAlpha" ]; then
  pass "New window still in same group ('ProjectAlpha')"
else
  fail "New window group is '$DIR_WIN_GROUP', expected 'ProjectAlpha'"
fi

if [ "$DIR_WIN_COLOR" = "#e74c3c" ]; then
  pass "New window adopted directory color '#e74c3c' instead of parent's '#112233'"
else
  fail "New window color is '$DIR_WIN_COLOR', expected directory color '#e74c3c'"
fi

rm -rf "$SPECIAL_DIR" "$TEST_STATE_DIR"

# ─── Test 4: Another group (Beta) and mid-group insertion ───
echo ""
echo "--- Test 4: Another group (Beta) and mid-group insertion ---"

# Create two windows in group "ProjectBeta" with color "#445566"
tmux new-window -t "$SESSION_ID:" -n "beta-1"
B1_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')"
tmux set-window-option -t "$B1_ID" @tabby_group "ProjectBeta"
tmux set-window-option -t "$B1_ID" @tabby_color "#445566"

tmux new-window -t "$SESSION_ID:" -n "beta-2"
B2_ID="$(tmux display-message -p -t "$TEST_SESSION" '#{window_id}')"
tmux set-window-option -t "$B2_ID" @tabby_group "ProjectBeta"
tmux set-window-option -t "$B2_ID" @tabby_color "#445566"

# Focus B1
tmux select-window -t "$B1_ID"
B1_IDX="$(tmux display-message -p -t "$B1_ID" '#{window_index}')"

# Create new window while on B1
"$PROJECT_ROOT/bin/tabby" new-window -session "$SESSION_ID" 2>/dev/null || true

# Expected: new window is inserted at B1_IDX + 1
EXPECTED_NEW_IDX=$((B1_IDX + 1))
B_NEW_ID="$(tmux display-message -p -t "$TEST_SESSION:$EXPECTED_NEW_IDX" '#{window_id}')"
B_NEW_GROUP="$(tmux show-window-options -t "$B_NEW_ID" -v @tabby_group 2>/dev/null || echo "")"
B_NEW_COLOR="$(tmux show-window-options -t "$B_NEW_ID" -v @tabby_color 2>/dev/null || echo "")"

if [ "$B_NEW_GROUP" = "ProjectBeta" ]; then
  pass "New window created in user's current group ('ProjectBeta')"
else
  fail "New window group is '$B_NEW_GROUP', expected 'ProjectBeta'"
fi

if [ "$B_NEW_COLOR" = "#445566" ]; then
  pass "New window inherited user's current window color ('#445566')"
else
  fail "New window color is '$B_NEW_COLOR', expected '#445566'"
fi

if [ "$B_NEW_ID" != "$B1_ID" ] && [ "$B_NEW_ID" != "$B2_ID" ]; then
  pass "New window placed immediately below B1 (at index $EXPECTED_NEW_IDX)"
else
  fail "New window was not inserted after B1"
fi

B2_NEW_IDX="$(tmux display-message -p -t "$B2_ID" '#{window_index}')"
if [ "$B2_NEW_IDX" -eq "$((EXPECTED_NEW_IDX + 1))" ]; then
  pass "B2 shifted down to index $B2_NEW_IDX"
else
  fail "B2 did not shift down, at index $B2_NEW_IDX"
fi

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] || exit 1
