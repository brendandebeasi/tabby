# Manual QA Test Results

## Test Date: 2026-01-29
## Tester: User
## Build: After commits 92361dc and 1ef415c

## Summary

**Partial Success** - The garbled text issue is fixed, but mouse/keyboard input still has problems after toggle.

### What's Fixed ✅
- No garbled text like `[?2004l` appears (main issue resolved)
- Works with multiple attached clients
- Works in both Ghostty and kitty
- Recovers from renderer crashes
- Sidebar toggles on/off correctly

### What's Still Broken ❌
- Mouse clicks don't work in sidebar after toggle
- Keyboard input doesn't work in content panes after toggle
- Only works in terminal that wasn't focused during toggle
- Issue persists across all test scenarios

## Detailed Results

### Scenario 1: Basic Toggle (Single Client)
**Result**: PARTIAL PASS
- ✅ Sidebar toggles correctly
- ✅ No garbled text
- ❌ Mouse clicks in sidebar don't work after toggle
- ❌ Keyboard input in content panes doesn't work after toggle
- 🔍 Note: Originally pane 0.0 had focus, after toggle pane 0.1 has focus

### Scenario 2: Multiple Clients
**Result**: PARTIAL PASS
- ✅ Both terminals show sidebar state correctly
- ✅ No garbled text in either terminal
- ❌ Mouse/keyboard only work in terminal that wasn't focused during toggle
- 🔍 Key finding: Focus state affects which terminal works

### Scenario 3: Rapid Toggle
**Result**: PARTIAL PASS
- ✅ System remains stable
- ✅ No garbled text or corruption
- ❌ Mouse clicks in sidebar don't work
- ❌ Keyboard input in content panes doesn't work

### Scenario 4: Different Terminal Emulators
**Result**: PARTIAL PASS
- ✅ Same behavior in both Ghostty and kitty
- ❌ Input issues present in both emulators

### Scenario 5: Renderer Crash Recovery
**Result**: MIXED
- ✅ Sidebar recovers after crash
- ✅ Mouse/keyboard work after crash recovery
- ❌ Doesn't fix the input issue from windows that had sidebar toggled

## Analysis

### Progress Made
1. **Primary issue fixed**: No more garbled escape sequences
2. **Stability improved**: Can toggle without terminal corruption
3. **Multi-client support**: All clients see consistent state

### Remaining Issue
The mouse/keyboard input problem appears to be related to **focus state**:
- Input only works in terminals that weren't focused during toggle
- Focus changes from pane 0.0 to 0.1 after toggle
- Crash recovery works, suggesting the issue is with toggle process, not renderer

### Hypothesis
The toggle process may be:
1. Changing focus in an unexpected way
2. Not properly restoring input modes for the focused terminal
3. Missing some terminal state restoration for the active client

## Next Steps

1. **Investigate focus handling** during toggle
2. **Check if tmux client refresh** is needed for focused terminal
3. **Review toggle script** for focus-related commands
4. **Consider adding explicit focus restoration** after toggle

## Terminal Versions
- Ghostty: (version not specified)
- kitty: (version not specified)
- tmux: (version not specified)

## Conclusion

The fix successfully resolved the garbled text issue, which was the primary complaint. However, a secondary issue with input handling remains. The pattern suggests this is related to focus state management during the toggle process.

**Recommendation**: Iterate on the fix to address the focus/input issue while preserving the garbled text fix.
