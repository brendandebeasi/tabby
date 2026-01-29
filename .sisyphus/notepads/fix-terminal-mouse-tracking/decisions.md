# Architectural Decisions

## [2026-01-29] Cleanup Strategy

### Decision: Rely on BubbleTea's Built-in Cleanup
**Rationale**: BubbleTea already handles terminal state management correctly. Our manual interventions are causing the problem, not solving it.

**Implementation**:
1. Remove all manual escape sequence writing from toggle script
2. Remove resetTerminal() function from sidebar-renderer
3. Let BubbleTea's natural cleanup handle everything
4. Increase wait time from 0.2s to 0.5s to allow graceful shutdown

**Trade-offs**:
- Pro: Simple, matches original working code
- Pro: No garbled text
- Pro: Works across all terminal emulators
- Con: Requires trusting BubbleTea's cleanup (but it's proven to work)

## [2026-01-29] Fallback Mechanism

### Decision: Keep resetTerminalModes() as Fallback
**Rationale**: While BubbleTea handles normal cleanup, we need a fallback for crashed renderers.

**Implementation**:
- Kept `resetTerminalModes()` function in daemon (toggles tmux mouse option)
- Removed call on startup (not needed for normal operation)
- Can be called manually if needed for recovery
- Toggle script also has tmux mouse toggle as final fallback

**Recovery Path**:
1. Normal case: BubbleTea cleans up automatically
2. If stuck: Toggle tabby off/on (script toggles tmux mouse)
3. If still stuck: Restart daemon (can call resetTerminalModes if needed)
4. Last resort: Detach/reattach tmux session
