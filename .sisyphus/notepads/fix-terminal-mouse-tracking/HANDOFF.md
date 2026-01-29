# HANDOFF: Manual QA Testing Required

## Implementation Status: ✅ COMPLETE

All code changes have been implemented, verified, and committed:
- ✅ Toggle script fixed (removed manual escape sequences)
- ✅ Sidebar-renderer simplified (BubbleTea handles cleanup)
- ✅ Daemon startup cleaned up
- ✅ Binaries built successfully
- ✅ Changes committed (commit 92361dc)

## What Remains: Manual QA Testing

**Why Manual Testing is Required**:
This is a TUI application where the bug manifests as a user experience issue:
- Mouse clicks not registering
- Keyboard input not working
- Garbled text appearing in terminal

These issues CANNOT be detected by:
- Static analysis
- Unit tests
- Build verification
- LSP diagnostics

They REQUIRE actual human interaction with a terminal emulator.

## How to Test

### Quick Test (5 minutes)
1. Build: `go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/ && go build -o bin/tabby-daemon ./cmd/tabby-daemon/`
2. Start tmux with tabby enabled
3. Toggle OFF: `ctrl+b Tab`
4. Toggle ON: `ctrl+b Tab`
5. Verify:
   - ✓ No garbled text like `[?2004l` appears
   - ✓ Can click in sidebar
   - ✓ Can type in panes

### Comprehensive Test (15 minutes)
Follow the detailed test plan in `test-plan.md`:
- Basic toggle
- Multiple clients
- Rapid toggle
- Different terminal emulators
- Crash recovery

Record results in `test-results.md`.

## Expected Outcome

**BEFORE the fix**:
- Garbled text `[?2004l` appeared in terminals
- Mouse clicks didn't work after toggle
- Keyboard input didn't work after toggle
- Had to detach/reattach tmux to fix

**AFTER the fix**:
- No garbled text
- Mouse works immediately after toggle
- Keyboard works immediately after toggle
- No need to detach/reattach

## If Tests Pass

Mark these items as complete in the plan file:

**Definition of Done**:
- [x] Can toggle tabby with `ctrl+b Tab` multiple times
- [x] Mouse clicks work in all panes after toggle
- [x] Keyboard input works in all panes after toggle
- [x] No garbled escape sequences appear
- [x] Works with multiple attached clients

**Task 5 Acceptance Criteria**:
- [x] All scenarios work correctly
- [x] No terminal corruption in any case
- [x] No garbled text appears
- [x] Mouse and keyboard work after toggle

**Final Checklist**:
- [x] Toggle works without breaking other terminals
- [x] No detach/reattach required
- [x] Works with multiple clients
- [x] No visible escape sequences

## If Tests Fail

1. Document the failure in `test-results.md`:
   - What scenario failed?
   - What was the symptom?
   - Screenshot if possible
   - Terminal emulator and version

2. Check if it's a new issue or the original bug:
   - Original bug: garbled text, mouse/keyboard not working
   - New issue: something else broke

3. If original bug persists:
   - Check if binaries were rebuilt
   - Check if tmux config is correct
   - Try detach/reattach as workaround
   - May need to iterate on the fix

4. If new issue appeared:
   - Document what broke
   - Consider reverting changes
   - Investigate new root cause

## Files to Update After Testing

1. `test-results.md` - Record test outcomes
2. `.sisyphus/plans/fix-terminal-mouse-tracking.md` - Mark checkboxes
3. `learnings.md` - Add any new insights from testing

## Contact

If you encounter issues or need clarification:
- Review `summary.md` for overview
- Check `learnings.md` for technical details
- See `decisions.md` for architectural rationale
- Consult `test-plan.md` for test scenarios

---

**Implementation work is complete. Ready for your testing!** 🚀
