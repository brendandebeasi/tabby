# Tabby Behavior Specification & Test Guide

This document specifies the expected behavior of the Tabby tmux plugin and provides test cases to validate correct operation.

## Table of Contents

1. [Sidebar Display Rules](#1-sidebar-display-rules)
2. [Window Lifecycle](#2-window-lifecycle)
3. [Pane Lifecycle](#3-pane-lifecycle)
4. [Group Behavior](#4-group-behavior)
5. [Mode Transitions](#5-mode-transitions)
6. [Edge Cases](#6-edge-cases)
7. [Test Procedures](#7-test-procedures)
8. [Pane Border Colors](#8-pane-border-colors)
9. [Context Menus](#9-context-menus)
10. [Pane Collapse/Expand](#10-pane-collapseexpand)

---

## 1. Sidebar Display Rules

### 1.1 Window Display

| Condition | Expected Display |
|-----------|------------------|
| Window with 1 pane | Show window only, no pane listing |
| Window with 2+ panes | Show window, then indented pane list below |
| Active window | Highlighted with `>` prefix and bright colors |
| Inactive window | Dimmed colors, no `>` prefix |

### 1.2 Pane Display

| Condition | Expected Display |
|-----------|------------------|
| Single pane in window | Pane NOT shown (redundant with window) |
| Multiple panes in window | Each pane shown indented with `|--` prefix |
| Active pane | Marked with `*` or highlight |
| Sidebar/utility panes | NEVER shown (filtered out) |

### 1.3 Tree Structure Example

```
Default
|-- > 0. my-project          <- Active window, single pane (pane not shown)
|--   1. another-window      <- Inactive, single pane
StudioDome
|-- > 2. SD|frontend         <- Active window with 2 panes
    |-- * 2.0 vim            <- Active pane (marked with *)
    |--   2.1 npm            <- Inactive pane
|--   3. SD|backend          <- Inactive, single pane
```

### 1.4 Utility Pane Filtering

These pane commands are ALWAYS filtered from display:
- `sidebar`
- `tabbar`
- `pane-bar`

---

## 2. Window Lifecycle

### 2.1 Window Creation

**Trigger**: `prefix + c`, `[+] New Tab` button, or `tmux new-window`

**Expected Behavior**:
1. New window created at next available index
2. Sidebar pane added to new window (if sidebar mode enabled)
3. All sidebars refresh to show new window
4. Window appears in Default group (unless created with prefix)
5. Automatic window naming enabled (shows running command)

**Validation**:
```bash
# Before: 3 windows (0, 1, 2)
tmux new-window
# After: 4 windows (0, 1, 2, 3), sidebar shows all 4
```

### 2.2 Window Selection

**Trigger**: Click in sidebar, `prefix + n/p`, `prefix + 1-9`, or `tmux select-window`

**Expected Behavior**:
1. Target window becomes active
2. Focus moves to main content pane (not sidebar)
3. All sidebars refresh to update highlight
4. Previous window's sidebar state preserved

**Validation**:
```bash
# Start on window 0
# Click window 2 in sidebar
# Result: Window 2 active, cursor in content pane, sidebar shows > on window 2
```

### 2.3 Window Rename

**Trigger**: Right-click > Rename, or `tmux rename-window`

**Expected Behavior**:
1. Window name updated
2. `automatic-rename` disabled for that window (locks manual name)
3. All sidebars refresh
4. Group assignment re-evaluated (name may now match different group)

**Validation**:
```bash
# Window named "project" in Default group
tmux rename-window "SD|project"
# Result: Window moves to StudioDome group, name locked
```

### 2.4 Window Close

**Trigger**: Middle-click, `x` key, or right-click > Kill

**Expected Behavior**:
1. Confirmation dialog shown: "Close window X? (y/n)"
2. On confirm (`y`): Window killed
3. tmux auto-selects another window
4. Windows renumber if `renumber-windows on`
5. All sidebars refresh to remove closed window
6. No orphan sidebar processes remain

**Validation**:
```bash
# 3 windows: 0, 1, 2
# Close window 1
# Result: 2 windows remain (0, 1 after renumber), sidebar shows 2 windows
```

---

## 3. Pane Lifecycle

### 3.1 Pane Creation (Split)

**Trigger**: `prefix + "`, `prefix + %`, right-click > Split, or `tmux split-window`

**Expected Behavior**:
1. New pane created in current window
2. If window has group prefix, `automatic-rename` stays off
3. Sidebar refreshes to show pane list (now 2+ panes)
4. Pane bar refreshes (if horizontal mode)

**Validation**:
```bash
# Window with 1 pane - sidebar shows window only
tmux split-window -h
# Result: Window now has 2 panes, sidebar shows both panes indented
```

### 3.2 Pane Selection

**Trigger**: Click pane in sidebar, or `tmux select-pane`

**Expected Behavior**:
1. Clicked pane becomes active
2. Sidebar refreshes to update pane highlight
3. If pane in different window, window also switches

**Validation**:
```bash
# Active: Window 0, Pane 0
# Click "Window 1, Pane 2" in sidebar
# Result: Window 1 active, Pane 2 active, sidebar highlights both
```

### 3.3 Pane Close

**Trigger**: `exit` command, `prefix + x`, or `tmux kill-pane`

**Expected Behavior**:
1. Pane exits/closes
2. If other panes remain: Sidebar refreshes, pane list updated
3. If no panes remain (only sidebar): **Window auto-closes**
4. Pane count in sidebar updates

**Validation**:
```bash
# Window with 2 content panes + sidebar
# Close pane 1
# Result: Window now shows 1 content pane, pane list hidden in sidebar

# Window with 1 content pane + sidebar
# Close content pane
# Result: Window closes entirely (cleanup_orphan_sidebar.sh)
```

### 3.4 Break Pane to New Window

**Trigger**: Right-click pane > Break to New Window

**Expected Behavior**:
1. Pane moved to new window
2. Original window's pane count decreases
3. New window created with broken pane
4. All sidebars refresh

**Validation**:
```bash
# Window 0 with 2 panes
# Break pane 1 to new window
# Result: Window 0 has 1 pane, new Window 1 created with broken pane
```

---

## 4. Group Behavior

### 4.1 Group Assignment

Windows are assigned to groups via the `@tabby_group` tmux window option:

```bash
# Set a window's group
tmux set-window-option -t :1 @tabby_group "StudioDome"

# Remove group assignment (moves to Default)
tmux set-window-option -t :1 -u @tabby_group
```

**Rules**:
1. Windows with `@tabby_group` set are placed in that group
2. Windows without `@tabby_group` (or with unknown group) go to Default
3. Groups only shown if they have windows
4. Default group always appears first

### 4.2 Group Display Order

1. Default (always first if has windows)
2. Other groups in config.yaml order

### 4.3 Moving Windows Between Groups

**Trigger**: Right-click > Move to Group > [Group Name]

**Expected Behavior**:
1. `@tabby_group` window option set to group name
2. Window appears in new group immediately
3. Window name remains unchanged (no prefixes)
4. All sidebars refresh

### 4.4 Remove from Group

**Trigger**: Right-click > Move to Group > Remove from Group

**Expected Behavior**:
1. `@tabby_group` window option unset
2. Window moves to Default group
3. Window name remains unchanged
4. All sidebars refresh

---

## 5. Mode Transitions

### 5.1 Sidebar Mode (Vertical)

**State**: `@tabby_sidebar = "enabled"`

**Characteristics**:
- Left-side pane in each window running `sidebar` binary
- tmux status bar disabled
- Full mouse interaction (click, right-click, middle-click)
- Pane list visible for multi-pane windows

### 5.2 Horizontal Mode

**State**: `@tabby_sidebar = "horizontal"`

**Characteristics**:
- Top pane running `tabbar` binary
- tmux status bar disabled
- Limited mouse support

### 5.3 Disabled Mode

**State**: `@tabby_sidebar = "disabled"` or empty

**Characteristics**:
- No sidebar/tabbar panes
- tmux native status bar enabled
- Tab rendering via `render-status` and `render-tab`

### 5.4 Mode Toggle (prefix + Tab)

**Current: Enabled -> Disabled**:
1. Kill all sidebar panes across session
2. Set state to "disabled"
3. Enable tmux status bar

**Current: Disabled -> Enabled**:
1. Set state to "enabled"
2. Disable tmux status bar
3. Kill any tabbar panes
4. Add sidebar to all windows

---

## 6. Edge Cases

### 6.1 Orphan Sidebar Cleanup

**Scenario**: Last content pane in window closes, only sidebar remains

**Expected**: Window auto-closes within 0.2 seconds

**Test**:
```bash
# Create window with only 1 content pane + sidebar
# Close the content pane (exit or kill-pane)
# Result: Entire window should close
```

### 6.2 Rapid Window Operations

**Scenario**: Create/close multiple windows in quick succession

**Expected**: All sidebars eventually show correct state

**Test**:
```bash
for i in {1..5}; do tmux new-window; done
sleep 0.5
# Sidebar should show all 5 new windows
for i in {1..3}; do tmux kill-window; done
sleep 0.5
# Sidebar should show 2 remaining windows
```

### 6.3 Session Detach/Reattach

**Scenario**: Detach from session, then reattach

**Expected**: Sidebar state restored (enabled/disabled preserved)

**Test**:
```bash
# Enable sidebar mode
# Detach: prefix + d
# Reattach: tmux attach
# Result: Sidebar still present in all windows
```

### 6.4 Window Renumbering

**Scenario**: Close window in middle of sequence with `renumber-windows on`

**Expected**: Sidebar updates to show new indices

**Test**:
```bash
# Windows: 0, 1, 2, 3
# Close window 1
# Result: Windows renumber to 0, 1, 2; sidebar shows correct indices
```

### 6.5 Config Hot Reload

**Scenario**: Edit config.yaml while sidebar running

**Expected**: Sidebar reloads config and re-renders

**Test**:
```bash
# Edit config.yaml (change a color)
# Save file
# Result: Sidebar should update colors without restart
```

### 6.6 Kill Current Window

**Scenario**: Kill the window you're currently viewing

**Expected**:
1. Window closes
2. tmux auto-selects another window
3. That window's sidebar shows correctly

**Test**:
```bash
# On window 2 of 4
# Right-click window 2 > Kill > y
# Result: Now on window 1 or 3, sidebar updated
```

---

## 7. Test Procedures

### 7.1 Manual Test Checklist

Run through each test and mark pass/fail:

#### Window Tests
- [ ] Create new window - appears in sidebar
- [ ] Select window via click - switches correctly
- [ ] Select window via Alt+number - switches correctly
- [ ] Rename window - name updates, locks auto-rename
- [ ] Kill window via middle-click - confirmation, then closes
- [ ] Kill window via menu - confirmation, then closes
- [ ] Kill current window - switches to another window

#### Pane Tests
- [ ] Split horizontal - pane list appears in sidebar
- [ ] Split vertical - pane list appears in sidebar
- [ ] Select pane via click - pane becomes active
- [ ] Close pane (2+ remain) - pane list updates
- [ ] Close last content pane - window closes
- [ ] Break pane to window - new window created

#### Group Tests
- [ ] New window - appears in Default group
- [ ] Move to StudioDome - gets SD| prefix, moves to group
- [ ] Remove prefix - moves back to Default
- [ ] Rename with prefix - moves to matching group

#### Mode Tests
- [ ] Toggle sidebar on - sidebars appear, status bar off
- [ ] Toggle sidebar off - sidebars gone, status bar on
- [ ] Detach/reattach - mode preserved

#### Edge Case Tests
- [ ] Close orphan window - auto-closes
- [ ] Rapid create/delete - settles to correct state
- [ ] Config hot reload - updates without restart

### 7.2 Automated Test Script

```bash
#!/usr/bin/env bash
# tests/e2e/test_sidebar_behavior.sh
# Automated behavioral tests for Tabby sidebar

set -e

PASS=0
FAIL=0

log_pass() { echo "[PASS] $1"; ((PASS++)); }
log_fail() { echo "[FAIL] $1"; ((FAIL++)); }

# Setup: Ensure sidebar mode enabled
tmux set-option @tabby_sidebar "enabled"

# Test 1: Window Creation
echo "Test 1: Window Creation"
BEFORE=$(tmux list-windows | wc -l)
tmux new-window -d -n "test-window"
sleep 0.3
AFTER=$(tmux list-windows | wc -l)
if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    log_pass "Window created"
else
    log_fail "Window not created (before=$BEFORE, after=$AFTER)"
fi

# Test 2: Window Rename
echo "Test 2: Window Rename"
tmux rename-window -t :test-window "renamed-test"
sleep 0.2
NAME=$(tmux display-message -t :renamed-test -p '#{window_name}')
if [ "$NAME" = "renamed-test" ]; then
    log_pass "Window renamed"
else
    log_fail "Window rename failed (got: $NAME)"
fi

# Test 3: Pane Split
echo "Test 3: Pane Split"
tmux select-window -t :renamed-test
BEFORE=$(tmux list-panes -t :renamed-test | wc -l)
tmux split-window -h -t :renamed-test
sleep 0.2
AFTER=$(tmux list-panes -t :renamed-test | wc -l)
if [ "$AFTER" -eq "$((BEFORE + 1))" ]; then
    log_pass "Pane split"
else
    log_fail "Pane split failed"
fi

# Test 4: Pane Close
echo "Test 4: Pane Close"
BEFORE=$(tmux list-panes -t :renamed-test | wc -l)
tmux kill-pane -t :renamed-test.1
sleep 0.2
AFTER=$(tmux list-panes -t :renamed-test | wc -l)
if [ "$AFTER" -eq "$((BEFORE - 1))" ]; then
    log_pass "Pane closed"
else
    log_fail "Pane close failed"
fi

# Test 5: Window Kill
echo "Test 5: Window Kill"
BEFORE=$(tmux list-windows | wc -l)
tmux kill-window -t :renamed-test
sleep 0.3
AFTER=$(tmux list-windows | wc -l)
if [ "$AFTER" -eq "$((BEFORE - 1))" ]; then
    log_pass "Window killed"
else
    log_fail "Window kill failed"
fi

# Test 6: Group Assignment via Rename
echo "Test 6: Group Assignment"
tmux new-window -d -n "group-test"
sleep 0.2
tmux rename-window -t :group-test "SD|group-test"
sleep 0.2
NAME=$(tmux display-message -t ":SD|group-test" -p '#{window_name}' 2>/dev/null || echo "NOT_FOUND")
if [ "$NAME" = "SD|group-test" ]; then
    log_pass "Group prefix applied"
else
    log_fail "Group prefix failed (got: $NAME)"
fi
tmux kill-window -t ":SD|group-test" 2>/dev/null || true

# Test 7: Orphan Cleanup
echo "Test 7: Orphan Cleanup"
tmux new-window -n "orphan-test"
sleep 0.3
WINDOW_EXISTS=$(tmux list-windows -F '#{window_name}' | grep -c "orphan-test" || true)
if [ "$WINDOW_EXISTS" -gt 0 ]; then
    # Kill all non-sidebar panes
    for pane in $(tmux list-panes -t :orphan-test -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | cut -d: -f1); do
        tmux kill-pane -t "$pane"
    done
    sleep 0.5
    WINDOW_EXISTS=$(tmux list-windows -F '#{window_name}' | grep -c "orphan-test" || true)
    if [ "$WINDOW_EXISTS" -eq 0 ]; then
        log_pass "Orphan window auto-closed"
    else
        log_fail "Orphan window NOT auto-closed"
        tmux kill-window -t :orphan-test 2>/dev/null || true
    fi
else
    log_fail "Test window not created"
fi

# Summary
echo ""
echo "================================"
echo "Results: $PASS passed, $FAIL failed"
echo "================================"

exit $FAIL
```

### 7.3 Running Tests

```bash
# Make test executable
chmod +x tests/e2e/test_sidebar_behavior.sh

# Run from tmux session with Tabby enabled
./tests/e2e/test_sidebar_behavior.sh
```

---

## Appendix: Signal Flow Diagram

```
User Action
    |
    v
tmux hook fires (after-select-window, pane-exited, etc.)
    |
    v
Shell script runs (on_window_select.sh, cleanup_orphan_sidebar.sh)
    |
    v
Script reads PID file (/tmp/tabby-sidebar-$SESSION.pid)
    |
    v
SIGUSR1 sent to sidebar process
    |
    v
BubbleTea receives signal, sends refreshMsg
    |
    v
Update() handles refreshMsg, calls ListWindowsWithPanes()
    |
    v
View() re-renders with new window/pane data
    |
    v
Terminal displays updated sidebar
```

---

## 8. Pane Border Colors

### 8.1 Dynamic Border Coloring

When `auto_border` is enabled in config, pane borders are colored dynamically based on group theme:

| Pane State | Border Color |
|------------|--------------|
| Active pane in active window | Full group color (e.g., `#3498db`) |
| Inactive pane | Lightened color (15% towards white) |
| Any pane in inactive window | Lightened color |

### 8.2 Configuration

Enable in `config.yaml`:

```yaml
pane_header:
  auto_border: true
```

### 8.3 Per-Pane Overrides

The daemon sets `pane-border-style` per-pane using `tmux set-option -p -t <pane_id>`. This allows different borders on different panes in the same window.

---

## 9. Context Menus

### 9.1 Menu Locations

| Click Target | Menu Type | Actions |
|--------------|-----------|---------|
| Window name | Window Menu | Rename, Kill, Move to Group, New Window |
| Window indicator (left edge) | Indicator Menu | Toggle indicators (busy, input, etc.) |
| Pane entry | Pane Menu | Select, Split, Break to Window, Kill |
| Group header | Group Menu | Collapse/Expand, New Window in Group |
| Sidebar header area | Settings Menu | Toggle options, Resize sidebar |

### 9.2 Triggers

- **Right-click (Mouse button 3)**: Opens context menu
- **Middle-click (Mouse button 2)**: Quick actions (kill window/pane with confirmation)

### 9.3 Menu Display

Menus are displayed using `tmux display-menu` at the click position. The daemon calculates menu position based on pane coordinates and mouse position.

### 9.4 Troubleshooting

If context menus don't appear:
1. Check that `MouseDown3Pane` is unbound: `tmux show-keys | grep MouseDown3`
2. Enable daemon debug mode: `TABBY_DEBUG=1` and check logs
3. Verify sidebar-renderer is receiving mouse events

---

## 10. Pane Collapse/Expand

### 10.1 Collapsed State

Windows can have their pane list collapsed in the sidebar to save space:

| State | Display |
|-------|---------|
| Expanded (default) | Window + all panes shown indented |
| Collapsed | Window only, pane count shown as badge |

### 10.2 Toggle Collapse

- **Click**: Toggle pane visibility via click on window entry
- **Right-click > Collapse/Expand**: Via context menu
- **tmux option**: `tmux set-window-option @tabby_collapsed on`

### 10.3 Persistence

Collapsed state is stored in `@tabby_collapsed` window option and persists across sidebar refreshes.

---

## Appendix: Common Issues

### Sidebar Not Refreshing
- Check PID file exists: `ls /tmp/tabby-daemon-*.pid`
- Verify process running: `ps aux | grep tabby-daemon`
- Manually signal: `kill -USR1 $(cat /tmp/tabby-daemon-*.pid)`

### Duplicate Groups Showing
- Kill daemon/renderers: `pkill -f tabby-daemon && pkill -f sidebar-renderer`
- Rebuild binaries: `./scripts/install.sh`
- Toggle sidebar off/on: `prefix + Tab` twice

### Window Not Closing After Last Pane
- Check cleanup script: `cat scripts/cleanup_orphan_sidebar.sh`
- Verify hook exists: `tmux show-hooks -g | grep pane-exited`
- Manual test: `tmux list-panes` should show only sidebar
