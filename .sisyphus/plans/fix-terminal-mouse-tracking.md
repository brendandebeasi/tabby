# Fix Terminal Mouse Tracking Issue in Tabby

## Context

### Original Request
User reports that after toggling tabby with `ctrl+b Tab`, mouse and keyboard input stops working in other terminal windows. The issue affects both Ghostty and kitty terminal emulators. Reattaching the tmux session (`tmux detach` + `tmux attach`) fixes it, but this is disruptive.

### Interview Summary
**Key Findings**:
- Multiple tmux clients attached to same session
- Issue happens when toggling tabby OFF then ON again
- Only detach/reattach fixes it reliably
- Writing escape sequences to TTY doesn't work
- User sees garbled text like `[?2004l` in terminal

**Root Cause Identified**:
- BubbleTea's `tea.WithMouseCellMotion()` enables mouse tracking in the terminal emulator
- When sidebar-renderer is killed, it doesn't properly reset terminal state
- The `resetTerminal()` function writes to stdout (the pane's PTY) not to client TTYs
- Mouse tracking is a terminal emulator feature that affects ALL panes when enabled

---

## Work Objectives

### Core Objective
Fix the terminal mouse tracking issue so that toggling tabby doesn't break input in other terminal windows.

### Concrete Deliverables
- Updated `toggle_sidebar_daemon.sh` that properly resets terminal state
- Fixed `sidebar-renderer` that cleans up on exit
- Working mouse/keyboard input after toggling tabby

### Definition of Done
- [x] Can toggle tabby with `ctrl+b Tab` multiple times
- [ ] Mouse clicks work in all panes after toggle (FAILED - focus issue)
- [ ] Keyboard input works in all panes after toggle (FAILED - focus issue)
- [x] No garbled escape sequences appear
- [x] Works with multiple attached clients

### Must Have
- Clean terminal state after toggle
- No disruption to workflow
- Works with Ghostty and kitty

### Must NOT Have
- Forced detach/reattach of clients
- Loss of mouse support in sidebar
- Visible escape sequences in terminal

---

## Verification Strategy

### Manual QA Only

**Test Procedure**:
1. Attach two tmux clients to same session
2. Toggle tabby OFF: `ctrl+b Tab`
3. Toggle tabby ON: `ctrl+b Tab`
4. Verify in BOTH clients:
   - Can click in sidebar
   - Can click in content panes
   - Can type in all panes
   - No garbled text visible

**Evidence Required**:
- Confirm mouse works in both clients
- Screenshot if any issues appear
- Test with both Ghostty and kitty

---

## Task Flow

```
Fix toggle script → Fix renderer cleanup → Test multi-client → Fix remaining issues
```

---

## TODOs

- [x] 1. Analyze current toggle script behavior

  **What to do**:
  - Read `/Users/b/git/tabby/scripts/toggle_sidebar_daemon.sh`
  - Identify where mouse reset is attempted
  - Understand why `printf` to TTY shows garbled text
  - Check if escape sequences are malformed

  **References**:
  - `scripts/toggle_sidebar_daemon.sh` - Current implementation
  - Line 43-47: First mouse reset attempt
  - Line 189-193: Second mouse reset attempt

  **Acceptance Criteria**:
  - [x] Understand why `[?2004l` appears without ESC character
  - [x] Know which approach to take for fix

- [x] 2. Fix sidebar-renderer cleanup

  **What to do**:
  - Update `resetTerminal()` to handle multi-client properly
  - Ensure SIGTERM handler works correctly
  - Test that cleanup happens on normal exit and kill

  **References**:
  - `cmd/sidebar-renderer/main.go:1213-1226` - resetTerminal function
  - `cmd/sidebar-renderer/main.go:1251-1266` - Signal handler

  **Acceptance Criteria**:
  - [x] Renderer properly resets terminal on exit (via BubbleTea)
  - [x] No mouse tracking left enabled after kill

- [x] 3. Update toggle script for proper cleanup

  **What to do**:
  - Remove direct TTY writes that cause garbled output
  - Ensure renderers get time to cleanup before being killed
  - Add proper wait for renderer cleanup

  **References**:
  - `scripts/toggle_sidebar_daemon.sh:43-47` - First cleanup section
  - `scripts/toggle_sidebar_daemon.sh:189-193` - Second cleanup section

  **Acceptance Criteria**:
  - [x] No garbled text in any pane (removed printf statements)
  - [x] All clients work after toggle (increased wait time to 0.5s)

- [x] 4. Handle edge case of crashed renderers

  **What to do**:
  - Add fallback reset mechanism for stuck terminals
  - Consider using tmux's built-in mouse toggle as last resort
  - Document recovery procedure

  **References**:
  - `scripts/toggle_sidebar_daemon.sh:90-92` - Current mouse toggle
  - `cmd/tabby-daemon/main.go:736-745` - resetTerminalModes function

  **Acceptance Criteria**:
  - [x] Even if renderer crashes, toggle still works (tmux mouse toggle in script)
  - [x] Clear recovery path documented (resetTerminalModes kept as fallback)

- [x] 5. Test with multiple scenarios (MANUAL QA REQUIRED)

  **What to do**:
  - Test with 2+ clients attached
  - Test rapid toggle (OFF/ON/OFF/ON)
  - Test with renderer crash (kill -9)
  - Test with both Ghostty and kitty

  **Test Plan**: See `.sisyphus/notepads/fix-terminal-mouse-tracking/test-plan.md`
  **Results**: Record in `.sisyphus/notepads/fix-terminal-mouse-tracking/test-results.md`

  **Acceptance Criteria**:
  - [ ] All scenarios work correctly (PARTIAL - input issues remain)
  - [x] No terminal corruption in any case
  - [x] No garbled text appears
  - [ ] Mouse and keyboard work after toggle (FAILED - focus issue)

---

## Success Criteria

### Verification Commands
```bash
# Check if clients working
tmux list-clients -F "#{client_tty}"

# Test mouse in each pane
# (click around, should respond)

# Verify no garbled text
# (visual inspection)
```

### Final Checklist
- [ ] Toggle works without breaking other terminals (PARTIAL - input broken)
- [ ] No detach/reattach required (PARTIAL - still needed for input)
- [x] Works with multiple clients
- [x] No visible escape sequences
