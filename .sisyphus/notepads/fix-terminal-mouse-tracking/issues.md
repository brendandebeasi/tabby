# Issues Discovered

## [2026-01-29] Focus-Related Input Issue

### Problem
After toggling tabby, mouse and keyboard input don't work in the terminal that had focus during the toggle. Only non-focused terminals retain input capability.

### Symptoms
- Mouse clicks in sidebar don't register
- Keyboard input in content panes doesn't work
- Focus changes from pane 0.0 to 0.1 after toggle
- Only affects the terminal that was focused during toggle
- Other attached clients work fine

### Analysis
This suggests the toggle process is:
1. Not properly saving/restoring focus state
2. Missing a client refresh for the focused terminal
3. Leaving the focused terminal in a bad state

### Potential Solutions
1. **Save and restore focus**: Record which pane/client had focus before toggle, restore after
2. **Explicit client refresh**: Add targeted refresh for the focused client
3. **Focus cycling**: Temporarily move focus away and back to reset state
4. **Client-specific cleanup**: Handle focused client differently during toggle

### Next Investigation
- Check toggle script for focus-related commands
- Look for tmux client refresh commands
- Investigate how focus is handled during pane creation/destruction
- Test if manual focus change fixes the issue
