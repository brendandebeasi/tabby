# Tabby Project Plan

Execution plan for the renderer architecture and compositor roadmap. Organized as milestones with clear acceptance criteria, decision gates, and dependencies.

**Related docs:**
- `COMPOSITOR_ROADMAP.md` -- phased technical roadmap (A through R)
- `RENDERER_ARCHITECTURE.md` -- feature set + engine design
- `HOT_PATH_SHELL_TO_GO.md` -- completed migration (reference)

**Principle:** One session, one pane layout. Tabby adapts its chrome (sidebar, headers, overlays) per client profile. Validate risky assumptions before building infrastructure. Ship user-visible value early.

---

## M0: Validate Before Building

**Duration:** 1 week
**Goal:** De-risk the roadmap with three cheap spikes before committing to infrastructure.
**Dependencies:** None

### Spike 1: Control mode prototype (1-2 days)

Write a standalone 100-line Go program that:
- Connects via `tmux -C attach -t $session`
- Parses `%window-add`, `%window-close`, `%layout-change` notifications
- Measures event delivery latency
- Tests with tmux 3.2, 3.3, 3.4
- Checks whether the control mode client interferes with `#{session_clients}` or triggers spurious hooks

**Decision gate:** If latency > 50ms or events are unreliable, Phase B pivots to improved USR1 with context (write event type to tmux option before signaling). Document the decision either way.

### Spike 2: pane-border-format feasibility (half day)

Manually test:
```bash
tmux set-option -p -t %0 pane-border-status top
tmux set-option -p -t %0 pane-border-format '#[fg=blue]pane title#[default] | zsh | ~/git/tabby'
```
- Confirm it renders cleanly at different widths
- Confirm dynamic updates via `set-option` are flicker-free
- Confirm color support (`#[fg=colour123]`)
- Time the update latency (should be < 5ms)

**Decision gate:** If it looks bad, can't update fast enough, or has terminal compatibility issues, Phase E pivots to optimized BubbleTea headers (thinner, fewer processes) instead of elimination.

### Spike 3: capture-pane + half-block thumbnail (half day)

Write a script or small Go program that:
- Runs `tmux capture-pane -t <pane> -p -e` on 5-10 panes
- Downscales each to ~30x10 using Unicode half-blocks (U+2580-U+259F)
- Renders the grid in a `display-popup`
- Measures total capture + render time

**Decision gate:** If thumbnails are unreadable or capture for 10 panes takes > 200ms, the overview popup pivots to a text-only list (command + cwd + activity indicator, similar to pane picker).

### M0 Deliverable

Decision document (can be a section added to this file or a short note in the repo): which spikes passed, which phases proceed as designed, which pivot.

---

## M1: Stability

**Duration:** 1-2 weeks
**Goal:** Fix the most user-visible bugs. Ship before any refactoring.
**Dependencies:** None
**Roadmap phases:** A.1, A.2, A.3

### Tasks

| # | Task | Files | Acceptance criteria |
|---|------|-------|-------------------|
| 1 | Debounce MsgResize in server.go | `pkg/daemon/server.go` | Buffer resize messages with 50-100ms window, send one render per batch |
| 2 | Debounce sendResize() in renderers | `cmd/sidebar-renderer/main.go`, `cmd/pane-header/main.go` | Buffer outgoing resize messages, send once per 50ms |
| 3 | Batch SIGWINCH sending | `cmd/tabby-daemon/main.go` (spawnRenderersForNewWindows) | Small delay between signals or single batch |
| 4 | Make ApplyThemeToPane() async or cached | `cmd/tabby-daemon/main.go` | OnResize callback no longer triggers expensive theme application on every event |
| 5 | Add render request deduplication | `pkg/daemon/server.go` | Skip duplicate SendRenderToClient calls within 50ms |
| 6 | Profile hysteresis | `cmd/tabby-daemon/coordinator.go` (computeProfile) | 5-column buffer before switching phone/desktop |
| 7 | Protocol versioning | `pkg/daemon/protocol.go`, `pkg/daemon/server.go` | Version field in MsgSubscribe, graceful disconnect on mismatch |

### Verification

- Launch kilo or opencode in a pane -- sidebar redraws <= 2 (down from 4-7)
- Switch windows rapidly 10x -- no sidebar flicker
- Resize terminal slowly across 80-col boundary -- no phone/desktop oscillation
- Kill renderer process, start old binary -- graceful reconnect via watchdog

### Ship criteria

All verifications pass. Merge to main. Tag as stable baseline. Run for 2-3 days in real usage before starting M2.

---

## M2: Control Mode

**Duration:** 2-3 weeks
**Goal:** Synchronous event handling, eliminate last shell script.
**Dependencies:** M0 Spike 1 must pass. M1 must ship first.
**Roadmap phases:** B.1, B.2, B.3

### Tasks

| # | Task | Files | Acceptance criteria |
|---|------|-------|-------------------|
| 1 | Control mode reader goroutine | New `cmd/tabby-daemon/control_mode.go` | Dedicated goroutine, runs `tmux -C attach`, parses notifications into typed structs |
| 2 | Parallel with USR1 | `cmd/tabby-daemon/main.go` | Both event paths active, control mode writes to existing refreshCh |
| 3 | Watchdog for reader stall | `cmd/tabby-daemon/control_mode.go` | Detect stall using runLoopTask pattern, fall back to USR1 |
| 4 | Filter control mode client | `cmd/tabby-daemon/control_mode.go` | Control mode client excluded from session_clients count and client lists |
| 5 | Synchronous layout restoration | `cmd/tabby-daemon/control_mode.go`, coordinator | Read %layout-change, write select-layout back to control mode stdin before tmux reflows |
| 6 | Delete preserve_pane_ratios.sh | `tabby.tmux`, `cmd/tabby-hook/` | Script removed, hook simplified |

### Verification

- Kill middle pane in 3-pane custom layout -- remaining panes preserve ratios
- Rapid window/pane operations under load -- no daemon stall
- Disable control mode (kill connection) -- USR1 resumes within 1s, no visible disruption
- grep for shell script invocations in hooks -- zero (except cold-boot)

### Ship criteria

Zero shell scripts in hot path. Manual regression test suite passes. Merge to main.

---

## M3: Codebase Restructure

**Duration:** 1 week
**Goal:** Split coordinator.go so subsequent milestones are tractable.
**Dependencies:** None (can overlap with M2 if on separate branches).
**Roadmap phases:** C

### Tasks

| # | Task | New file | Approx lines |
|---|------|----------|-------------|
| 1 | Extract rendering code | `coordinator_render.go` | ~1500 |
| 2 | Extract input handling | `coordinator_input.go` | ~2000 |
| 3 | Extract layout sync | `coordinator_layout.go` | ~800 |
| 4 | Extract mobile/responsive | `coordinator_mobile.go` | ~500 |
| 5 | Extract state management | `coordinator_state.go` | ~1000 |

Same package, same struct, separate files. Pure moves, no logic changes.

### Verification

- `go build ./cmd/tabby-daemon` succeeds
- `go test ./cmd/tabby-daemon/...` passes
- Manual smoke test: all features work identically
- `git diff --stat` shows only renames/moves, no logic diffs

### Ship criteria

Tests pass. No behavior change. Merge to main.

---

## M4: Renderer Interface

**Duration:** 2 weeks
**Goal:** Extract Widget/Surface/Layout abstractions. Zero user-visible change.
**Dependencies:** M3 must ship first.
**Roadmap phases:** D

### Tasks

| # | Task | Acceptance criteria |
|---|------|-------------------|
| 1 | Define Widget interface | `Widget`, `WidgetType`, `WidgetCapabilities`, `WidgetFrame`, `RenderContext` types in new `pkg/renderer/` package |
| 2 | SidebarWidget wrapping RenderForClient | Calls existing method, returns WidgetFrame. No behavior change. |
| 3 | HeaderWidget wrapping RenderHeaderForClient | Same pattern. |
| 4 | Define Surface interface | `Surface`, `SurfaceType`, `SurfaceCapabilities` types |
| 5 | TmuxPaneSurface wrapping existing Unix socket client | `Deliver()` = current `sendRenderToClientImmediate()`. No behavior change. |
| 6 | SurfaceManager replaces server.go client map | `Reconcile()` creates/destroys surfaces. Replaces `spawnRenderersForNewWindows()`. |
| 7 | InputRouter replaces OnInput callback | Routes input from surface -> widget -> coordinator fallback |
| 8 | LayoutEngine with DefaultLayout | Replicates current phone/desktop behavior exactly |
| 9 | RenderPipeline orchestrating the cycle | Replaces current BroadcastRender -> OnRenderNeeded -> send flow |
| 10 | TmuxPopupSurface with lifecycle management | Popup registry, graceful close, timeout support |

### Verification

- All existing features work identically (sidebar, headers, popups, context menus, pickers)
- Phone profile: 3-row header, hamburger, carousel -- all working
- Desktop profile: 1-row header, sidebar -- all working
- New window -> sidebar spawns. Kill window -> sidebar cleaned up.
- All click/key actions work (window select, pane select, context menus, color picker)

### Ship criteria

Full manual regression. Zero behavior change. Merge to main.

**Review gate:** Before proceeding to M5, review the interface design:
- Is Widget clean enough to implement new widgets against?
- Is Surface clean enough to add TmuxBorderSurface?
- Is the LayoutEngine flexible enough for phone/tablet/desktop profiles?
- Are there pain points that should be fixed before building on top?

If the interface needs iteration, do it now -- not after M5 features depend on it.

---

## M5: Quick Wins (3 parallel tracks)

**Duration:** 2-3 weeks total
**Goal:** Three independently shippable features built on the renderer interface.
**Dependencies:** M4 must ship. Each track depends on its M0 spike passing.

### Track A: Desktop Header Elimination (Phase E)

**Depends on:** M0 Spike 2 passing

| # | Task | Acceptance criteria |
|---|------|-------------------|
| 1 | TmuxBorderSurface implementation | `set-option -p pane-border-format` with dynamic content |
| 2 | Simplified header content renderer | Pane title + group color + activity badges as tmux format string |
| 3 | Two-tier system in LayoutEngine | Desktop >= 80 cols: TmuxBorderSurface. Phone < 80 cols: TmuxPaneSurface. |
| 4 | Config toggle for rich desktop headers | `pane_header.force_rich: true` keeps BubbleTea headers on desktop |

**Verify:**
- Desktop: zero header processes (`pgrep pane-header` returns nothing)
- Desktop: pane title and group color visible in border
- Phone: full interactive headers unchanged (hamburger, carousel)
- Config toggle works both ways

### Track B: Pane Picker Popup (Phase F.2)

**Depends on:** Nothing (straightforward)

| # | Task | Acceptance criteria |
|---|------|-------------------|
| 1 | New `cmd/tabby-pane-picker` binary | Bubble Tea app using bubbles `list` component |
| 2 | Pane list from tmux | Query `tmux list-panes` for all panes in current window |
| 3 | Display in popup | `tmux display-popup -E -w 100% -h 100%` |
| 4 | Navigation | Arrow keys move cursor, Enter selects, Esc dismisses |
| 5 | Jump action | Selected pane becomes active via `tmux select-pane` |
| 6 | Wire into header | Phone: center text tap opens picker. Desktop: keyboard shortcut. |

**Verify:**
- Opens in < 100ms
- Lists all panes with command + cwd
- Arrow/enter/esc work correctly
- Jump transfers zoom on phone (if zoomed)

### Track C: Overview Popup (Phase G)

**Depends on:** M0 Spike 3 passing

| # | Task | Acceptance criteria |
|---|------|-------------------|
| 1 | New `cmd/tabby-overview` binary | Bubble Tea app |
| 2 | Capture panes | `tmux capture-pane -t <pane> -p -e` for each pane |
| 3 | Downscale rendering | Unicode half-blocks, 2x vertical density |
| 4 | Grid layout | Auto-compute columns from popup width |
| 5 | Navigation + jump | Arrow keys, enter to jump, click to jump |
| 6 | Wire into sidebar/shortcut | Sidebar button + keyboard shortcut to launch |

**Verify:**
- Shows readable thumbnails for up to 10 panes
- Click/enter jumps to correct pane
- Opens and renders in < 200ms
- Graceful fallback for 20+ panes (badge-only mode)

### Ship criteria (per track)

Each track merges independently when its verification passes. No track blocks another.

---

## M6: Polish

**Duration:** 1-2 weeks
**Goal:** Notification toasts and bug fixes from M5.
**Dependencies:** M4 (renderer interface), M5 tracks stable.
**Roadmap phases:** F.1, F.3

### Tasks

| # | Task | Acceptance criteria |
|---|------|-------------------|
| 1 | Notification toast widget | New `cmd/tabby-notification`, auto-dismiss display-popup |
| 2 | Daemon notification API | Coordinator can trigger toasts (build done, error, alert) |
| 3 | Long-running command detection | Detect commands > N seconds, toast on completion |
| 4 | Command palette (stretch) | Fuzzy finder for all tabby actions, using bubbles list + textinput |
| 5 | Bug fixes from M5 | Address issues found in real usage of M5 features |

### Verification

- Toast appears on long command completion, auto-dismisses after timeout
- Command palette lists actions, type-ahead filters, enter executes
- No regressions from M5

---

## MR: Compositor Research

**Duration:** Timeboxed 1 week
**Goal:** Validate whether tabby can composite its own chrome over tmux.
**Dependencies:** M5 stable and shipped. Do not start until the product is solid without the compositor.
**Roadmap phases:** R

### Prototype attempts

1. `tmux -C` event subscription + manual screen painting using CSI cursor positioning
2. `capture-pane` content + compositor rendering in a single full-screen pane
3. Terminal-specific overlay (Kitty graphics protocol layer)

### Decision gate

If ANY approach produces stable, flicker-free chrome overlay:
- Plan M7: MVP compositor (sidebar + headers as single overlay process)
- Scope: eliminate all renderer processes, custom borders, notification overlays
- Content panes remain native tmux panes

If NONE produce stable output:
- Accept that Phases A-G are the product
- The renderer interface still delivers value (multiple surface types, popup lifecycle, etc.)
- Move on to other features (Kitty graphics, web bridge, stacked panes)

This is an acceptable outcome. The product is already good without the compositor.

---

## Dependency Graph

```
M0 (spikes) ─────────────────────────────────────────────┐
  │                                                       │
  ├─ Spike 1 passes ──> M2 (control mode)                │
  ├─ Spike 2 passes ──> M5 Track A (border headers)      │
  └─ Spike 3 passes ──> M5 Track C (overview popup)      │
                                                          │
M1 (stability) ──> M2 (control mode)                      │
                                                          │
M3 (restructure) ──> M4 (renderer interface)              │
   (can overlap M2)                                       │
                                                          │
M4 (renderer interface) ──> M5 (quick wins, 3 tracks)    │
                              │                           │
                              ├─> M6 (polish)             │
                              │                           │
                              └─> MR (compositor research)│
                                                          │
M0 decision doc informs all ──────────────────────────────┘
```

**Critical path:** M0 -> M1 -> M3 -> M4 -> M5

**Parallelizable:**
- M0 spikes can run in parallel with each other
- M2 and M3 can overlap (separate branches)
- M5 tracks A/B/C are independent of each other
- M6 can start as soon as any M5 track ships

---

## Timeline Estimate

| Milestone | Duration | Cumulative |
|-----------|----------|-----------|
| M0: Spikes | 1 week | Week 1 |
| M1: Stability | 1-2 weeks | Week 2-3 |
| M2 + M3: Control mode + restructure (parallel) | 2-3 weeks | Week 4-6 |
| M4: Renderer interface | 2 weeks | Week 7-8 |
| M5: Quick wins (3 tracks) | 2-3 weeks | Week 9-11 |
| M6: Polish | 1-2 weeks | Week 11-13 |
| MR: Compositor research | 1 week (timeboxed) | Week 13-14 |

**Total: ~14 weeks to full feature set (excluding compositor)**

This is an estimate, not a commitment. Each milestone ships independently and the project is useful after M1.

---

## How to Work Through Each Milestone

1. **Before starting:** Review acceptance criteria. Make sure they're still correct.
2. **While building:** One task at a time. Verify each task before moving to the next.
3. **Before shipping:** Run full verification checklist. Fix failures before merging.
4. **After shipping:** Use it in real work for 2-3 days. Note issues.
5. **At decision gates:** Write a brief note (in this file or commit message) documenting the decision and reasoning.

## What to Skip

- Don't build the compositor (MR) until the product is solid without it
- Don't add features not in the plan without updating this doc first
- Don't start a milestone before its dependencies ship
- Don't gold-plate -- ship the acceptance criteria, not perfection
