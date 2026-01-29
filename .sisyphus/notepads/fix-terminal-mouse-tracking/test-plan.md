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
	YES
5. Check for garbled text in any pane (should be NONE)

6. Press `ctrl+b Tab` to toggle ON
7. Verify sidebar reappears
	YES, 0.0 originally had focus, 0.1 has focus
8. Test mouse clicks in sidebar (should work)
	NO
9. Test keyboard input in content panes (should work)
	NO

**Expected**: No garbled text, all input works

### Scenario 2: Multiple Clients
**Steps**:
1. Start tmux session in terminal 1
2. Attach to same session in terminal 2
3. In terminal 1: Press `ctrl+b Tab` to toggle OFF
4. Verify both terminals show sidebar gone
	YES
5. Check for garbled text in BOTH terminals (should be NONE)
	OK
6. In terminal 2: Press `ctrl+b Tab` to toggle ON
7. Verify both terminals show sidebar back
8. Test mouse/keyboard in BOTH terminals (should work)
	ONLY TERMINAL NOT FOCUS IN

**Expected**: Both clients work correctly, no garbled text

### Scenario 3: Rapid Toggle
**Steps**:
1. Start tmux session
2. Rapidly toggle: OFF, ON, OFF, ON, OFF, ON (6 toggles in ~10 seconds)
3. Check for garbled text (should be NONE)
4. Test mouse clicks in sidebar
	Nope
5. Test keyboard input in content panes
	Nope

**Expected**: System remains stable, no corruption

### Scenario 4: Different Terminal Emulators
**Steps**:
1. Test Scenario 1 in Ghostty
	Issue here
2. Test Scenario 1 in kitty
	Issue here
3. Test Scenario 2 with one Ghostty + one kitty client

**Expected**: Works in both terminal emulators

### Scenario 5: Renderer Crash Recovery
**Steps**:
1. Start tmux session with sidebar
2. Find sidebar-renderer PID: `ps aux | grep sidebar-renderer`
3. Kill it: `kill -9 <PID>`
4. Toggle OFF then ON
5. Verify sidebar comes back
	Yes it does
6. Test mouse/keyboard (should work)
	Yes it does, but doesn't fix the other issue from the window that had the sidebar hidden/shown

**Expected**: Recovers gracefully from crash

## Success Criteria
- [ ] No garbled text like `[?2004l` appears in any scenario
	On
- [ ] Mouse clicks work in sidebar after toggle
	No
- [ ] Keyboard input works in all panes after toggle
	No
- [ ] Works with multiple attached clients
	Yes
- [ ] Works in both Ghostty and kitty
	Yes
- [ ] Recovers from renderer crashes
	Yes

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
