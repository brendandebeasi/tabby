# Fix Terminal Mouse Tracking - Summary

## Problem
After toggling tabby sidebar with `ctrl+b Tab`, mouse and keyboard input stopped working in terminal windows. User had to detach and reattach tmux session to fix it.

## Root Cause
Manual escape sequence writing to TTYs caused garbled text and didn't properly reset terminal state. BubbleTea already handles terminal cleanup automatically - our "fixes" were breaking it.

## Solution Implemented
**Simplify and trust BubbleTea**:
1. Removed all manual `printf` escape sequences from toggle script
2. Removed `resetTerminal()` function from sidebar-renderer
3. Removed premature `resetTerminalModes()` call from daemon startup
4. Increased cleanup wait time from 0.2s to 0.5s
5. Let BubbleTea handle all terminal state management

## Files Changed
- `scripts/toggle_sidebar_daemon.sh` - Removed printf statements, increased wait time
- `cmd/sidebar-renderer/main.go` - Removed resetTerminal() function and all calls
- `cmd/tabby-daemon/main.go` - Removed startup call to resetTerminalModes()

## Verification
- ✅ Code compiles successfully
- ✅ Both binaries build without errors
- ✅ Script syntax is valid
- ✅ Changes committed to git
- ⏳ Manual QA testing pending

## Testing Required
See `test-plan.md` for comprehensive test scenarios:
- Basic toggle (single client)
- Multiple clients attached
- Rapid toggle stress test
- Different terminal emulators (Ghostty, kitty)
- Renderer crash recovery

## Expected Outcome
- No garbled text like `[?2004l` appears
- Mouse clicks work in sidebar after toggle
- Keyboard input works in all panes after toggle
- Works with multiple attached clients
- No need to detach/reattach tmux

## Fallback Mechanisms
1. Toggle script has tmux mouse toggle as fallback
2. `resetTerminalModes()` function kept in daemon for manual recovery
3. Last resort: detach/reattach still works if needed

## Next Action
**User must perform manual QA testing** following the test plan and record results.

---

## Implementation Complete - Awaiting Manual QA

**Status**: All code changes implemented and committed. Manual testing required.

**Blocker**: Task 5 and all Definition of Done items require human interaction with terminal emulator. Cannot be automated.

**Handoff**: See `HANDOFF.md` for complete testing instructions.

**Quick Test**: 
```bash
# Rebuild binaries
go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/
go build -o bin/tabby-daemon ./cmd/tabby-daemon/

# Test toggle
ctrl+b Tab  # OFF
ctrl+b Tab  # ON

# Verify: no garbled text, mouse works, keyboard works
```

**Next Action**: User performs manual QA and marks remaining checkboxes.

### Final Code Improvement
Updated `cmd/tabby-daemon/main.go` to also refresh all clients in its recovery function. This ensures that if you use the manual recovery (via daemon), it fixes the focus issue just like the toggle script does.
