# Learnings

## [2026-01-29] Initial Investigation

### Root Cause Discovery
- BubbleTea's mouse tracking affects the ENTIRE terminal emulator, not just the tmux pane
- When sidebar-renderer is killed, mouse tracking stays enabled globally
- Writing escape sequences directly to TTYs causes **garbled text** like `[?2004l` to appear
- The original code (before our fixes) **didn't have manual terminal reset** - it relied on BubbleTea's natural cleanup

### Key Insight
**Our attempted fixes are causing the problem.** The garbled text appears because:
- `printf '\x1b[?2004l' > /dev/ttys020` writes escape sequences
- These sequences appear as literal text instead of being processed
- This corrupts terminal state further

### What Works
- BubbleTea automatically sends mouse disable sequences when exiting normally
- SIGTERM allows graceful shutdown
- No manual TTY manipulation = no garbled text
- Simple approach matches original working implementation

### What Doesn't Work
- Writing escape sequences to client TTYs directly
- Using `tmux send-keys` with escape sequences
- Trying to manually reset terminal modes from shell scripts

## [2026-01-29] Implementation Complete

### Changes Made
1. **scripts/toggle_sidebar_daemon.sh**:
   - Removed manual `printf` escape sequences at lines 44 and 55
   - Increased wait time from 0.2s to 0.5s for graceful cleanup
   - Kept tmux mouse toggle as fallback mechanism

2. **cmd/sidebar-renderer/main.go**:
   - Removed entire `resetTerminal()` function
   - Removed all calls to `resetTerminal()`
   - Simplified signal handler to just send tea.Quit()
   - BubbleTea now handles all terminal cleanup automatically

3. **cmd/tabby-daemon/main.go**:
   - Removed call to `resetTerminalModes()` on startup
   - Kept the function as it's a reasonable fallback (just toggles tmux mouse option)

### Build Verification
- `go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/` ✓
- `go build -o bin/tabby-daemon ./cmd/tabby-daemon/` ✓
- `bash -n scripts/toggle_sidebar_daemon.sh` ✓

### Why This Works
BubbleTea's `tea.NewProgram()` with `tea.WithMouseCellMotion()` automatically:
1. Enables mouse tracking when program starts
2. Disables mouse tracking when program exits (via SIGTERM or normal exit)
3. Handles all escape sequences correctly

By removing our manual interventions, we let BubbleTea do its job properly.

## [2026-01-29] Ready for Testing

### Implementation Status
All code changes complete:
- ✅ Toggle script fixed (no more printf to TTY)
- ✅ Sidebar-renderer simplified (BubbleTea handles cleanup)
- ✅ Daemon startup cleaned up (no premature reset)
- ✅ Binaries built successfully
- ✅ Changes committed

### Next Steps
Manual QA testing required. See test-plan.md for detailed scenarios.

**Why Manual Testing is Critical**:
- This is a TUI application with mouse/keyboard interaction
- Bug manifests as user experience issue (input not working)
- Need to verify across multiple terminal emulators
- Need to test multi-client scenarios
- Static analysis cannot catch these issues

**Test Plan Location**: `.sisyphus/notepads/fix-terminal-mouse-tracking/test-plan.md`
**Results Location**: `.sisyphus/notepads/fix-terminal-mouse-tracking/test-results.md`

## [2026-01-29] Manual QA Results

### Partial Success
The fix successfully eliminated garbled text, but revealed a secondary issue with input handling.

### What Worked
- ✅ No more `[?2004l` garbled text (primary issue fixed)
- ✅ BubbleTea cleanup is working correctly
- ✅ Multi-client state synchronization works
- ✅ Crash recovery works properly

### What Didn't Work
- ❌ Mouse clicks don't work in sidebar after toggle
- ❌ Keyboard input doesn't work in content panes after toggle
- ❌ Only works in terminal that wasn't focused during toggle

### Key Discovery
**Focus state matters**: The input issue only affects the terminal that had focus during toggle. Other attached clients work fine. This suggests the problem is with how we handle the actively focused client during toggle.

### New Hypothesis
The toggle process may need to:
1. Save focus state before killing renderers
2. Properly refresh the focused client after toggle
3. Explicitly restore input modes for the active terminal

### Interesting Observations
- Focus changes from pane 0.0 to 0.1 after toggle
- Crash recovery (kill -9) works fine, suggesting issue is in toggle script
- Both Ghostty and kitty affected equally (not terminal-specific)
