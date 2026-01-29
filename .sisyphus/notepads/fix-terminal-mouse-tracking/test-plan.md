# Manual Test Plan

## Prerequisites
1. Build binaries: `go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/ && go build -o bin/tabby-daemon ./cmd/tabby-daemon/`
2. Ensure tabby is enabled in tmux config
3. Have both Ghostty and kitty available for testing

## Test Scenarios

### Scenario 1: Basic Toggle (Single Client)
**Steps**:
1. Start tmux session
2. Verify sidebar is visible
3. Press `ctrl+b Tab` to toggle OFF
4. Verify sidebar disappears
5. Check for garbled text in any pane (should be NONE)
6. Press `ctrl+b Tab` to toggle ON
7. Verify sidebar reappears
8. Test mouse clicks in sidebar (should work)
9. Test keyboard input in content panes (should work)

**Expected**: No garbled text, all input works

### Scenario 2: Multiple Clients
**Steps**:
1. Start tmux session in terminal 1
2. Attach to same session in terminal 2
3. In terminal 1: Press `ctrl+b Tab` to toggle OFF
4. Verify both terminals show sidebar gone
5. Check for garbled text in BOTH terminals (should be NONE)
6. In terminal 2: Press `ctrl+b Tab` to toggle ON
7. Verify both terminals show sidebar back
8. Test mouse/keyboard in BOTH terminals (should work)

**Expected**: Both clients work correctly, no garbled text

### Scenario 3: Rapid Toggle
**Steps**:
1. Start tmux session
2. Rapidly toggle: OFF, ON, OFF, ON, OFF, ON (6 toggles in ~10 seconds)
3. Check for garbled text (should be NONE)
4. Test mouse clicks in sidebar
5. Test keyboard input in content panes

**Expected**: System remains stable, no corruption

### Scenario 4: Different Terminal Emulators
**Steps**:
1. Test Scenario 1 in Ghostty
2. Test Scenario 1 in kitty
3. Test Scenario 2 with one Ghostty + one kitty client

**Expected**: Works in both terminal emulators

### Scenario 5: Renderer Crash Recovery
**Steps**:
1. Start tmux session with sidebar
2. Find sidebar-renderer PID: `ps aux | grep sidebar-renderer`
3. Kill it: `kill -9 <PID>`
4. Toggle OFF then ON
5. Verify sidebar comes back
6. Test mouse/keyboard (should work)

**Expected**: Recovers gracefully from crash

## Success Criteria
- [ ] No garbled text like `[?2004l` appears in any scenario
- [ ] Mouse clicks work in sidebar after toggle
- [ ] Keyboard input works in all panes after toggle
- [ ] Works with multiple attached clients
- [ ] Works in both Ghostty and kitty
- [ ] Recovers from renderer crashes

## Failure Indicators
- ❌ Garbled escape sequences visible
- ❌ Mouse clicks don't register
- ❌ Keyboard input doesn't work
- ❌ Need to detach/reattach to fix

## How to Report Results
Update `.sisyphus/notepads/fix-terminal-mouse-tracking/test-results.md` with:
- Which scenarios passed/failed
- Screenshots of any issues
- Terminal emulator versions tested
- Any unexpected behavior
