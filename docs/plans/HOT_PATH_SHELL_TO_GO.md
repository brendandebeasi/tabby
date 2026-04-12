# Hot Path Shell-to-Go Migration

**STATUS: PHASE 1 + PHASE 3 COMPLETE** (commits `f50a0ed`, `ccc68f3`, `3bf1878`)

See `COMPOSITOR_ROADMAP.md` for the continuation (Phases 4-6 redesigned as Phases B-R).
See `RENDERER_ARCHITECTURE.md` for the complete feature set and renderer engine design.

## Requirements Summary

Consolidate the ~14 shell scripts that fire on every window switch, pane focus, and pane kill into the Go daemon. The daemon already has the state these scripts query (windows, panes, layouts, client geometry). Eliminating the shell middlemen cuts 10+ tmux round-trips and multiple process spawns per navigation event.

Design each migration step so the daemon's internal interfaces support future UI flexibility (profile-aware headers, alternative renderers, mobile popups per the existing Mobile PR1-3 plans).

## Acceptance Criteria

- [ ] `after-select-window` hook fires a single `run-shell` that signals the daemon (USR1). No other scripts chained. Daemon handles border color, status refresh, window history, pane dimming, ensure-sidebar, and status exclusivity internally. Measured: < 15ms from hook fire to daemon receiving signal (currently ~50-100ms for the full chain).
- [ ] `after-select-pane` hook fires a single `run-shell -b` that signals the daemon. Daemon handles layout save, pane dimming, and re-render internally. No `cycle-pane` binary invocation.
- [ ] `after-kill-pane` hook fires a single `run-shell` that signals the daemon. Daemon handles layout preservation, orphan cleanup, and status guard internally.
- [ ] `after-split-window` hook fires a single signal to daemon. Daemon handles window name preservation and layout save.
- [ ] `after-new-window` hook fires a single signal to daemon. Daemon handles group application, ensure-sidebar, status refresh.
- [ ] `cycle-pane --dim-only` logic absorbed into daemon's refresh loop. The `cycle-pane` binary is no longer invoked from hooks (can remain for manual user invocation).
- [ ] No regression in pane dimming behavior (inactive panes dim, active pane bright, sidebar/header panes excluded).
- [ ] No regression in window history tracking (closing a window returns to the previously-viewed window, not adjacent).
- [ ] No regression in layout preservation (killing a pane restores saved ratios for remaining panes).
- [ ] No regression in border coloring (active border matches tab group color).
- [ ] `@tabby_spawning` guard behavior preserved -- daemon skips hook processing during its own pane creation.
- [ ] All existing tmux keybindings continue to work (they invoke hooks which now just signal the daemon).
- [ ] Mobile PR1 design (`desiredPaneHeaderHeight`, `clientProfile`, `RunHeaderHeightSync`) can be implemented cleanly against the consolidated daemon without needing shell script changes.

## Architecture

### Current Flow (per window switch)
```
tmux hook -> run-shell on_window_select.sh  (5-7 tmux calls, sync)
          -> run-shell refresh_status.sh     (1 tmux call, sync)
          -> run-shell -b track_window_history.sh  (3 tmux calls)
          -> run-shell -b ensure_sidebar.sh        (5-10 tmux calls)
          -> run-shell -b enforce_status_exclusivity.sh (4-6 tmux calls)
          -> run-shell -b cycle-pane --dim-only    (5-8 tmux calls)
Total: 6 process spawns, 23-35 tmux round-trips
```

### Target Flow (per window switch)
```
tmux hook -> run-shell "kill -USR1 $(cat /tmp/tabby-daemon-$SID.pid)"
Daemon receives USR1:
  -> handleWindowSelect() internally (0 tmux calls for state, 2-3 for writes)
Total: 1 process spawn (the run-shell), 2-3 tmux calls from daemon
```

### Daemon Internal Design

New methods on Coordinator/main loop:

```
handleWindowSelect(activeWindowID)     -- replaces on_window_select.sh + track_window_history.sh
handlePaneSelect(activePaneID)         -- replaces on_pane_select.sh + save_pane_layout.sh  
handlePaneKill(windowID)               -- replaces preserve_pane_ratios.sh
handleSplitWindow(windowID)            -- replaces signal_sidebar.sh + preserve_window_name.sh
handleNewWindow(windowID)              -- replaces apply_new_window_group.sh + ensure_sidebar.sh
applyPaneDimming(activeWindowID)       -- replaces cycle-pane --dim-only
enforceStatusExclusivity()             -- replaces enforce_status_exclusivity.sh
updateBorderColor(windowID)            -- replaces border logic in on_window_select.sh
saveWindowLayout(windowID, layout)     -- replaces save_pane_layout.sh
restoreWindowLayout(windowID)          -- replaces preserve_pane_ratios.sh
trackWindowHistory(windowID)           -- replaces track_window_history.sh
```

State that moves from tmux global options into Coordinator fields:
- `@tabby_window_history` -> `Coordinator.windowHistory []string`
- `@tabby_layout_${WID}` -> `Coordinator.savedLayouts map[string]string`
- `@tabby_skip_preserve_${WID}` -> `Coordinator.skipPreserve map[string]bool`
- `@tabby_close_select_window/index` -> `Coordinator.closeSelectTarget`

State that stays in tmux (needed by other tmux features):
- `pane-active-border-style`, `pane-border-style` (tmux renders these)
- Per-pane `window-style` for dimming (tmux renders these)
- `automatic-rename` per window
- `status` on/off

### Hook Registration Changes

Phase 2 of `tabby.tmux` simplifies all hook registrations. The daemon PID is read once and cached:

```bash
# All hooks reduce to a single daemon signal
SIGNAL_CMD="kill -USR1 \$(cat /tmp/tabby-daemon-#{session_id}.pid 2>/dev/null) 2>/dev/null || true"
SIGNAL_CMD_USR2="kill -USR2 \$(cat /tmp/tabby-daemon-#{session_id}.pid 2>/dev/null) 2>/dev/null || true"

tmux set-hook -g after-select-window "run-shell '$SIGNAL_CMD'"
tmux set-hook -g after-select-pane   "run-shell -b '$SIGNAL_CMD'"
tmux set-hook -g after-kill-pane     "run-shell '$SIGNAL_CMD'"
tmux set-hook -g after-split-window  "run-shell -b '$SIGNAL_CMD'"
tmux set-hook -g after-new-window    "run-shell '$SIGNAL_CMD'"
tmux set-hook -g window-linked       "run-shell '$SIGNAL_CMD'"
tmux set-hook -g window-unlinked     "run-shell '$SIGNAL_CMD'"
tmux set-hook -g client-attached     "run-shell '$SIGNAL_CMD'"
tmux set-hook -g client-active       "run-shell -b '$SIGNAL_CMD_USR2'"
tmux set-hook -g client-focus-in     "run-shell -b '$SIGNAL_CMD_USR2'"
tmux set-hook -g client-resized      "run-shell -b '$SIGNAL_CMD_USR2'"
tmux set-hook -g session-created     "run-shell '$SIGNAL_CMD'"
```

### Differentiating Hook Types

Today the daemon receives USR1 for "something changed, refresh." But `after-select-window` needs different handling than `after-kill-pane`. Two approaches:

**Option A: Single signal, daemon detects context.** Daemon queries `tmux display-message -p '#{window_id}|#{pane_id}'` on every USR1 and diffs against cached state to determine what changed. Simple hook config, but adds one tmux round-trip per signal.

**Option B: Multiple signals with context via tmux option.** Hook writes event type to a tmux option before signaling:
```bash
tmux set-option -g @tabby_event "window_select"; kill -USR1 ...
```
Daemon reads `@tabby_event` on USR1. Two tmux calls (one write from hook, one read from daemon) but the daemon knows exactly what happened.

**Recommended: Option A.** The daemon already queries active window/pane on every USR1 (line 1976 `updateActiveWindow()`). It can diff against `lastActiveWindow` and `lastActivePane` to detect what changed. No hook changes needed, no extra tmux state. The only case that needs special handling is `after-kill-pane` (layout restoration must happen before tmux reflows), which we handle by making `preserve_pane_ratios.sh` the last surviving sync shell script until we can replace it with a `control mode` approach in a future phase.

### Migration Exception: preserve_pane_ratios.sh

This script MUST run synchronously in the hook because tmux reflows pane geometry immediately after the hook returns. If the daemon handles it asynchronously (via USR1), the reflow happens before the layout is restored. Two options:

1. **Keep as sync shell script** (simplest, recommended for Phase 1). One shell script surviving is acceptable.
2. **tmux control mode** (future). The daemon subscribes to tmux events via `tmux -C` and can respond synchronously. This is a larger architectural change suitable for a later phase.

## Implementation Steps

### Step 1: Move pane dimming into daemon (highest impact, standalone)
**Files:** `cmd/tabby-daemon/main.go`, `cmd/tabby-daemon/coordinator.go`
**What:** Port `cycle-pane --dim-only` logic into a `Coordinator.applyPaneDimming()` method. Call it from the existing `refreshCh` handler after `BroadcastRender()`. The daemon already lists panes and knows which are system panes (sidebar/header).
**Key logic to port:**
- Filter content panes (exclude sidebar-renderer, pane-header by `pane_current_command`)
- Set `window-style bg=<dim_color>` on inactive content panes via `tmux set-option -p`
- Unset `window-style` on active content pane via `tmux set-option -p -u`
- Desaturate inactive border color from active border color
- Skip if `@tabby_spawning=1`
**Test:** Switch windows rapidly. Inactive panes should dim, active should be bright. No flicker. Verify sidebar/header panes are never dimmed.
**Remove from hooks:** Delete `cycle-pane --dim-only` invocation from `after-select-window` and `after-select-pane` hooks in `tabby.tmux`.

### Step 2: Move window history tracking into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Add `windowHistory []string` field to Coordinator. Port `track_window_history.sh` logic: on each window switch, push current window to front of history (dedup, cap at 20). Port `select_previous_window.sh` logic: on window close, select the most recent surviving window from history.
**Key logic to port:**
- Maintain LIFO stack of window IDs
- On window close: find first entry in history that still exists in `coordinator.windows`
- Call `tmux select-window -t <target>`
**Test:** Open 5 windows, visit them in order 1,3,5,2,4. Close window 4. Should return to window 2. Close window 2. Should return to window 5.
**Remove from hooks:** Delete `track_window_history.sh` and `select_previous_window.sh` invocations from hooks.

### Step 3: Move layout save/restore into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Add `savedLayouts map[string]string` to Coordinator. On every pane select, save `tmux display-message -p -t <window> '#{window_layout}'`. On pane kill, restore layout via `tmux select-layout`.
**Key logic to port:**
- `saveWindowLayout()`: query layout string, store in map keyed by window ID
- `restoreWindowLayout()`: on USR1 after pane count decreases, apply saved layout
- Skip save/restore during `@tabby_spawning`
**Note:** Layout save already happens in the daemon's `windowCheckTicker` (`saveLayoutsToDisk`). This step adds per-event saving for finer granularity.
**Test:** Create 3 panes in custom layout. Kill middle pane. Remaining panes should preserve their relative sizes.
**Remove from hooks:** Delete `save_pane_layout.sh` from `after-select-pane` and `after-split-window` hooks.

### Step 4: Move border color update into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Port the border-color-from-tab logic from `on_window_select.sh`. The daemon already knows each window's group color via the coordinator's color system.
**Key logic to port:**
- On window switch: read window's tab background color
- Set `pane-active-border-style fg=<color>` globally
- Clear per-window `@tabby_input` and `@tabby_bell` indicators
**Test:** Switch between windows in different groups. Active border should match the tab color of the active window's group.
**Remove from hooks:** Delete `on_window_select.sh` from `after-select-window` hook.

### Step 5: Move status exclusivity into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Port `enforce_status_exclusivity.sh`. When sidebar is enabled, status bar is off; when disabled, status bar is on.
**Key logic to port:**
- Check sidebar mode (already tracked by coordinator)
- Set `tmux set-option -g status on/off` based on mode
- Only change if current state differs from desired (idempotent)
**Test:** Toggle sidebar off. Status bar should appear. Toggle on. Status bar should disappear.
**Remove from hooks:** Delete `enforce_status_exclusivity.sh` from all hooks.

### Step 6: Move ensure-sidebar into daemon
**Files:** `cmd/tabby-daemon/main.go`
**What:** The daemon already spawns renderers via `spawnRenderersForNewWindows`. The `ensure_sidebar.sh` script mostly duplicates this with extra startup logic. With the daemon always running (guaranteed by watchdog + Phase 1 instant start), `ensure_sidebar.sh` is redundant for the hot path.
**Key logic to port:**
- On USR1: check all windows have renderers (already done in `spawnRenderersForNewWindows`)
- Start watchdog if daemon not running (keep this in `ensure_sidebar.sh` for the cold-start path only, not hot path)
**Test:** Create new window. Sidebar should appear without `ensure_sidebar.sh` running. Kill a renderer pane manually. It should respawn within one daemon tick (3s) or on next USR1.
**Remove from hooks:** Delete `ensure_sidebar.sh` from `after-select-window`, `after-new-window`, `client-active`, `client-focus-in`, `client-resized` hooks. Keep it only in `tabby.tmux` Phase 1 for cold boot.

### Step 7: Simplify hook registrations in tabby.tmux
**Files:** `tabby.tmux`
**What:** Replace the multi-script hook chains with single-signal hooks as described in the Architecture section. Only `after-kill-pane` retains a sync shell script (`preserve_pane_ratios.sh`) for layout restoration timing.
**Test:** Full regression test: window create, switch, split, kill pane, kill window, client attach/detach, resize. All behaviors should match pre-migration.

### Step 8: Move window name preservation into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Port `preserve_window_name.sh`. On split-window, if the window name contains "|" (group prefix), set `automatic-rename off` for that window.
**Key logic to port:**
- After detecting a new pane in a window, check window name
- If name contains group separator, lock automatic-rename
**Test:** Create a grouped window, split it. Window name should stay as group name, not change to the new pane's command.
**Remove from hooks:** Delete `preserve_window_name.sh` from `after-split-window` hook.

### Step 9: Move group application into daemon
**Files:** `cmd/tabby-daemon/coordinator.go`
**What:** Port `apply_new_window_group.sh`. When a new window is created via the managed new-window flow, apply the saved group.
**Key logic to port:**
- On new window detection, check `@tabby_new_window_group` and `@tabby_new_window_id`
- Apply group to the new window via `set-window-option @tabby_group`
- Clear the temp options
**Test:** Set active group to "dev". Create new window. It should inherit the "dev" group.
**Remove from hooks:** Delete `apply_new_window_group.sh` from `after-new-window` hook.

### Step 10: Clean up dead scripts
**Files:** `scripts/` directory, `tabby.tmux`
**What:** Remove or mark as deprecated:
- `on_window_select.sh` (replaced by Step 4)
- `on_pane_select.sh` (replaced by Steps 1,3)
- `track_window_history.sh` (replaced by Step 2)
- `save_pane_layout.sh` (replaced by Step 3)
- `enforce_status_exclusivity.sh` (replaced by Step 5)
- `preserve_window_name.sh` (replaced by Step 8)
- `apply_new_window_group.sh` (replaced by Step 9)
- `signal_sidebar.sh` (hooks now signal directly)
- `refresh_status.sh` (daemon calls `tmux refresh-client -S` directly)
**Keep:** `ensure_sidebar.sh` (cold-boot only), `preserve_pane_ratios.sh` (sync timing requirement), `cycle-pane` binary (manual user invocation).
**Test:** `grep -r` for any remaining references to deleted scripts. Ensure no broken paths.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Layout restoration timing (preserve_pane_ratios) | Pane sizes wrong after kill | Keep as sync shell script in Phase 1. Migrate to control mode in future phase. |
| Daemon crash during hook handling | All hooks become no-ops until watchdog restarts | Watchdog restarts daemon within 1s. Shell scripts were equally fragile (daemon signal failed silently). Add crash recovery for in-flight state (layouts, history). |
| Subtle behavior differences in ported logic | Visual regressions (dimming, borders, layout) | Port each step individually with manual testing. Keep old scripts available for A/B comparison during development. |
| Hook processing latency if daemon is busy | USR1 queued behind long operation | Daemon already has timeout watchdog for stalled tasks. USR1 handler is fast (just writes to channel). Processing happens in the main loop which has stall detection. |
| tmux option state divergence | Daemon's cached state disagrees with tmux | Periodic reconciliation via existing `refreshTicker` (30s) and `windowCheckTicker` (3s). Same risk exists today with shell scripts. |

## Testing Strategy

Each migration step ships with automated tests that verify the migrated behavior. Tests use the existing e2e framework (`tests/e2e/test_utils.sh`) with isolated tmux sessions via `-L` socket, ensuring no interference with the user's live session.

### Test file: `tests/e2e/test_hot_path_migration.sh`

Created alongside Step 1 and expanded with each subsequent step. Tests run against a fresh daemon in an isolated tmux session.

### Per-Step Test Coverage

| Step | Test | Assertion |
|------|------|-----------|
| 1 (dimming) | Switch windows, check per-pane `window-style` | Inactive content panes have `bg=<dim>`, active pane has no `window-style`, sidebar/header panes never dimmed |
| 1 (dimming) | Rapid window switching (10 switches in <1s) | No zombie dim styles, final state correct |
| 2 (history) | Visit windows 1,3,5,2,4 then close 4 | Active window becomes 2. Close 2 -> becomes 5. |
| 2 (history) | Close all but one window | No crash, remaining window is active |
| 3 (layout) | Create 3 panes, resize, kill middle pane | Remaining panes preserve relative sizes (layout string matches within tolerance) |
| 3 (layout) | Kill pane during spawning guard | Layout save skipped, no corruption |
| 4 (border) | Switch between windows with different group colors | `pane-active-border-style` fg matches expected group color |
| 4 (border) | Switch to ungrouped window | Border reverts to default color |
| 5 (status) | Toggle sidebar mode off | `tmux show-option -g status` returns `on` |
| 5 (status) | Toggle sidebar mode on | `tmux show-option -g status` returns `off` |
| 6 (ensure) | Create new window | Sidebar renderer pane exists within 1s |
| 6 (ensure) | Kill sidebar renderer pane | Respawns within daemon tick (3s) |
| 7 (hooks) | Verify hook registrations | Each hook has exactly 1 `run-shell` (no chained scripts) |
| 8 (name) | Split a grouped window | Window name still contains group prefix |
| 9 (group) | Create window with active group set | New window inherits group via `@tabby_group` |
| 10 (cleanup) | Grep for deleted script references | Zero matches in `tabby.tmux` and hook registrations |

### Go Unit Tests

Each `handle*` method added to the Coordinator gets a pure unit test in `cmd/tabby-daemon/coordinator_test.go` (or a new `coordinator_hotpath_test.go`). These test the logic without tmux:

- `TestWindowHistoryTracking` -- push/dedup/cap/evict logic
- `TestSavedLayoutStore` -- save/restore/skip-during-spawning
- `TestPaneDimmingFilter` -- correctly identifies content vs system panes
- `TestBorderColorFromGroup` -- color lookup and fallback to default
- `TestStatusExclusivity` -- mode -> status mapping

### Performance Benchmark

A benchmark test (`tests/e2e/bench_hot_path.sh`) measures:
1. **Hook-to-daemon latency:** Timestamp in `run-shell` vs `SIGNAL_USR1` event log entry. Target: < 15ms.
2. **tmux round-trips per window switch:** Count `tmux` substrings in event log between two consecutive `SIGNAL_USR1` entries. Target: 2-3 (down from 23-35).
3. **Process spawns:** Count `run-shell` invocations per hook via `tmux show-hooks -g` output. Target: 1 per hook.

### Running Tests

```bash
# Full e2e suite (includes hot path tests)
./tests/e2e/run_e2e.sh

# Hot path migration tests only
./tests/e2e/test_hot_path_migration.sh

# Go unit tests
go test ./cmd/tabby-daemon/... -run 'TestWindowHistory|TestSavedLayout|TestPaneDimming|TestBorderColor|TestStatusExclusivity'

# Performance benchmark
./tests/e2e/bench_hot_path.sh
```

## Verification Steps

After each step:
1. Build: `go build -o bin/tabby-daemon ./cmd/tabby-daemon`
2. Run Go unit tests: `go test ./cmd/tabby-daemon/...`
3. Run e2e tests: `./tests/e2e/test_hot_path_migration.sh`
4. Restart sidebar: `pkill -f tabby-daemon; sleep 0.5; rm -f /tmp/tabby-daemon-*; bash tabby.tmux`
5. Manual smoke test the specific behavior (listed per step)

After full migration (Step 10):
1. Run full e2e suite: `./tests/e2e/run_e2e.sh`
2. Run performance benchmark: `./tests/e2e/bench_hot_path.sh`
3. Profile hot-path latency in daemon event log (target: < 15ms hook-to-signal)
4. Verify tmux round-trips per window switch (target: 2-3, down from 23-35)
5. Verify process spawns per hook (target: 1, down from 6)
6. Test with multiple clients attached simultaneously
7. Test cold boot (new tmux session) still works via Phase 1 instant start

---

## Roadmap

This plan is Phase 1 of a broader performance and UI flexibility initiative.

### Phase 1: Hot Path Shell-to-Go (this plan)
**Goal:** Eliminate shell script overhead on every user navigation event.
**Scope:** ~14 hot-path scripts consolidated into daemon. Single-signal hooks.
**Outcome:** < 15ms hook latency, daemon as single source of truth for navigation state.

### Phase 2: Mobile Client Support (existing PRs 1-3)
**Goal:** Make tabby usable on narrow mobile clients (Blink on iOS).
**Scope:** Client profiles, profile-aware pane headers, hamburger menu, auto-zoom, pane carousel, pane picker popup.
**Depends on:** Phase 1 (client profile detection plugs into consolidated daemon methods).
**Plans:** `docs/plans/MOBILE_PR1_PROFILE_AND_HAMBURGER.md`, `MOBILE_PR2_AUTOZOOM_AND_CAROUSEL.md`, `MOBILE_PR3_PANE_PICKER.md`

### Phase 3: Remaining Shell Consolidation
**Goal:** Move non-hot-path shell scripts into Go where it improves reliability.
**Scope:** `toggle_sidebar.sh`, `toggle_sidebar_daemon.sh`, group management scripts, `kill_window.sh`, `kill_pane_wrapper.sh`, `split_from_content_pane.sh`.
**Rationale:** These run on user commands (not hooks) so latency is less critical, but consolidation reduces the shell script surface area and makes the daemon the authoritative controller of all UI state.

### Phase 4: tmux Control Mode Integration
**Goal:** Replace signal-based IPC with tmux control mode (`tmux -C`) for synchronous event handling.
**Scope:** Daemon subscribes to tmux events directly instead of receiving USR1 from hooks. Eliminates the last `run-shell` overhead. Enables synchronous layout restoration (finally replaces `preserve_pane_ratios.sh`).
**Risk:** Control mode is a different programming model. Needs careful prototyping.
**Outcome:** Zero shell scripts in the hot path. All hooks replaced by control mode subscriptions.

### Phase 5: Renderer Abstraction
**Goal:** Decouple rendering from tmux pane split-window.
**Scope:** Define a renderer interface in the daemon. Current tmux-pane renderers (sidebar-renderer, pane-header) implement this interface. Future renderers (single overlay pane, web UI, Kitty graphics protocol) can plug in without changing the daemon's core logic.
**Depends on:** Phase 1-3 (all UI state centralized in daemon). Phase 4 helpful but not required.
**Enables:** The "more control over window borders, headers, and layout" vision -- including attaching the same pane to multiple windows, custom border rendering, and non-tmux frontends.

### Phase 6: Advanced Layout Control
**Goal:** Go beyond tmux's built-in layout constraints.
**Scope:** Shared panes across windows (via `link-window` or daemon-managed virtual panes), custom border rendering, flexible header placement, responsive layouts that adapt beyond simple width thresholds.
**Depends on:** Phase 5 (renderer abstraction), Phase 2 (client profiles).
**Open questions:** Whether to fork/extend tmux vs build an overlay system. Deferred until Phases 1-5 reveal what tmux can't do.

## UI Flexibility Enablement

Each step moves state and logic into the Coordinator, which becomes the single source of truth for all UI decisions. This enables:

- **Mobile PR1 (client profiles):** `clientProfile` detection plugs directly into `applyPaneDimming()` and `updateBorderColor()` without shell script changes
- **Mobile PR2 (auto-zoom):** `RunZoomSync` calls alongside the new `handleWindowSelect()`/`handlePaneSelect()` methods
- **Alternative renderers:** All rendering decisions flow through the Coordinator, so a different frontend (web, overlay, kitty graphics) only needs to implement the renderer interface, not rewire hooks
- **Pane-as-window sharing:** With layout/history/dimming in the daemon, implementing `link-window` based pane sharing doesn't require any shell script coordination
