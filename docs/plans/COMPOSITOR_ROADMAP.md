# Tabby Compositor Roadmap: From tmux Plugin to Terminal Display Server

## Vision

Tabby evolves from a tmux sidebar plugin into a **compositing window manager for the terminal**. All clients share a single tmux session with one pane layout (standard tmux behavior). Tabby's value is in the chrome layer: sidebar, headers, overlays, notifications, and navigation tools that adapt per client profile (phone/tablet/desktop). The renderer architecture cleanly separates widgets (what to show) from surfaces (how to deliver it), enabling zero-process desktop headers, popup-based overlays, and eventually a compositor for custom borders and floating UI elements.

## What's Done

### Hot Path Migration (Phase 1) -- COMPLETE
- All ~14 hot-path shell scripts migrated to Go daemon (dimming, window history, layout save/restore, border color, status exclusivity, ensure-sidebar, hook simplification, window name preservation, group application)
- 42 dead shell scripts deleted, `tabby-hook` CLI added
- Remaining non-hot-path shell scripts also migrated to Go
- Commits: `f50a0ed`, `ccc68f3`, `3bf1878`

### Shell Consolidation (Phase 3) -- COMPLETE (pulled forward)
- Non-hot-path scripts (toggle, group mgmt, kill/split) also migrated in the same pass
- Commit: `3bf1878`

### Mobile UI Foundation -- PARTIALLY COMPLETE
- Client profile detection (phone/desktop by width) -- LIVE
- 2-row/3-row pane-header on phone -- LIVE
- Hamburger button with sidebar popup -- LIVE
- Carousel prev/next controls -- LIVE
- Header height sync (RunHeaderHeightSync) -- LIVE
- Responsive sidebar width (mobile/tablet/desktop breakpoints) -- LIVE
- Auto-zoom (RunZoomSync) -- INTENTIONALLY STUBBED (conflicts with multi-client)
- Pane picker popup -- NOT STARTED
- Header oscillation fix -- SHIPPED (`ae9c3a9`)

### What Remains from Original Plans
- `preserve_pane_ratios.sh` -- last synchronous shell script, blocked on control mode
- Resize debounce -- diagnosed in RESIZE_INVESTIGATION.md, not yet fixed
- Pane picker popup (`cmd/tabby-pane-picker`) -- designed but not built

---

## Roadmap

### Phase A: Stabilization
**Goal:** Fix the most user-visible bugs before adding features.
**Effort:** 1 week. **Risk:** Low.

#### A.1 Resize Debounce
The MsgResize path has zero debouncing (see RESIZE_INVESTIGATION.md). Every resize event triggers an immediate render, causing 4-7 redraws during TUI app startup.

Fixes (priority order):
1. **Debounce MsgResize in `pkg/daemon/server.go`** -- buffer resize messages with 50-100ms window, send ONE render per batch
2. **Debounce `sendResize()` in sidebar-renderer and pane-header** -- buffer outgoing resize messages, send once per 50ms
3. **Batch SIGWINCH sending** in `spawnRenderersForNewWindows()` -- small delay between signals or single batch
4. **Make `ApplyThemeToPane()` async or cached** -- the OnResize callback is expensive for rapid fires
5. **Add render request deduplication** -- skip duplicate SendRenderToClient calls within 50ms

Acceptance criteria:
- TUI app startup (kilo, opencode) causes <= 2 sidebar redraws (down from 4-7)
- No sidebar flicker on window switch
- Mobile header resize doesn't cascade

#### A.2 Profile Hysteresis
Add a 5-column buffer to `computeProfile` -- once a profile is set, require crossing the threshold by 5 columns in the opposite direction before switching. Prevents phone/desktop oscillation for clients near the 80-column boundary.

#### A.3 Protocol Versioning
Add a `version` field to `MsgSubscribe`. Daemon includes protocol version in `MsgRender`. Renderers that receive an unknown version gracefully disconnect and let the watchdog restart them with the correct binary. Prevents silent breakage during all subsequent phases.

---

### Phase B: Control Mode
**Goal:** Synchronous event handling, eliminate last shell script.
**Effort:** 2-3 weeks. **Risk:** Medium (new programming model).
**Depends on:** Phase A (stable resize handling before changing event model).

#### B.1 Control Mode Prototype (spike, 1-2 days)
Write a standalone 100-line Go prototype that:
- Connects via `tmux -C attach -t $session`
- Parses `%window-add`, `%window-close`, `%layout-change`, `%pane-mode-changed` notifications
- Measures event delivery latency
- Tests with tmux 3.2, 3.3, 3.4
- Validates that control mode client doesn't interfere with session_clients count or trigger spurious hooks

**Decision gate:** If event delivery is unreliable or latency > 50ms, redesign Phase B around improved USR1 with context (Option B from HOT_PATH plan) instead of control mode.

#### B.2 Control Mode Reader Goroutine
If prototype passes:
- New `cmd/tabby-daemon/control_mode.go` -- dedicated goroutine
- Run in PARALLEL with existing USR1 signaling (not replacing it yet)
- Parse notifications into typed structs, write to existing `refreshCh` channel with event type metadata
- Watchdog: detect reader stall using same pattern as `runLoopTask` (`main.go:1814-1864`), fall back to USR1
- Filter control mode client from client lists (it affects `#{session_clients}`)

#### B.3 Synchronous Layout Restoration
Wire `%layout-change` and `%window-close` events through control mode. Daemon reads pane-kill event, computes layout restoration, writes `select-layout` back to control mode stdin -- all synchronous, before tmux reflows.

Acceptance criteria:
- `preserve_pane_ratios.sh` deleted
- Zero shell scripts in the hot path
- Layout restoration correct after pane kill (no geometry corruption)
- Graceful fallback to USR1 if control mode connection drops

#### B.4 Gradual USR1 Deprecation
Once control mode is stable, migrate remaining hooks:
- `after-select-window`, `after-select-pane` -> control mode events
- `client-active`, `client-resized` -> control mode events
- Eventually: all hooks become `run-shell` that only acts as a "wake up" signal for edge cases

---

### Phase C: Coordinator Decomposition
**Goal:** Make the codebase tractable for subsequent phases.
**Effort:** 1 week. **Risk:** Low (mechanical refactor).
**Depends on:** Nothing (can be done anytime, but must happen before Phase D).

`coordinator.go` is 12,756 lines with 50+ fields and 6+ mutexes. Before adding renderer abstractions, split it:

- `coordinator_render.go` -- `RenderForClient`, `RenderHeaderForClient` (~1500 lines)
- `coordinator_input.go` -- `HandleInput`, all widget action handlers (~2000 lines)
- `coordinator_layout.go` -- width sync, zoom sync, header height sync (~800 lines)
- `coordinator_mobile.go` -- client profiles, responsive breakpoints, phone rendering
- `coordinator_state.go` -- window history, saved layouts, dimming, border color

Same package, same struct, separate files. This is purely organizational -- no interface changes, no behavior changes.

---

### Phase D: Renderer Interface
**Goal:** Decouple content generation from delivery mechanism.
**Effort:** 2 weeks. **Risk:** Low-Medium.
**Depends on:** Phase C (coordinator must be decomposed first).

#### D.1 RenderSurface Interface

```go
type RenderSurface interface {
    ID() string
    Type() SurfaceType  // sidebar, header, popup, overlay, web
    Capabilities() SurfaceCapabilities
    Deliver(payload *RenderPayload) error
    Dimensions() (width, height int)
    Close() error
}

type SurfaceCapabilities struct {
    SupportsColor    bool
    SupportsGraphics bool   // Kitty graphics protocol
    SupportsMouse    bool   // Mouse click/scroll events
    SupportsResize   bool
    MaxFPS           int
    ColorProfile     string // TrueColor, ANSI256, etc.
}

type SurfaceType string
const (
    SurfaceTmuxPane   SurfaceType = "tmux-pane"
    SurfaceTmuxPopup  SurfaceType = "tmux-popup"
    SurfaceTmuxBorder SurfaceType = "tmux-border"
    SurfaceWebSocket  SurfaceType = "websocket"
    SurfaceOverlay    SurfaceType = "overlay"
)
```

#### D.2 Backend Implementations

| Backend | Delivery | Use case |
|---------|----------|----------|
| `TmuxPaneSurface` | Unix socket to BubbleTea process in split-window | Current sidebar-renderer, pane-header |
| `TmuxPopupSurface` | Unix socket to BubbleTea process in display-popup | Phone sidebar popup (already exists) |
| `TmuxBorderSurface` | `set-option -p pane-border-format` | Desktop headers without processes |
| `WebSocketSurface` | WebSocket JSON to browser | Web UI (tabby-web-bridge) |

#### D.3 ContentRenderer Split
Different surfaces need different content:
- `TmuxPaneSurface` gets pre-rendered ANSI strings (current behavior)
- `TmuxBorderSurface` gets simplified tmux format strings
- `WebSocketSurface` gets structured JSON (content + regions + metadata)

The coordinator delegates to surface-appropriate content generators selected based on `SurfaceCapabilities`.

#### D.4 Popup Lifecycle Management
Current popups are fire-and-forget `exec.Command`. Add:
- Popup registry in daemon (track active popups per window)
- Graceful close when another popup opens
- Content update channel for long-lived popups
- Timeout/auto-dismiss support

---

### Phase E: Desktop Header Elimination
**Goal:** Replace BubbleTea header panes with zero-process tmux border headers on desktop.
**Effort:** 1-2 weeks. **Risk:** Medium (UX regression on desktop headers).
**Depends on:** Phase D (needs RenderSurface interface to implement cleanly).

Note: This was originally "Phase C" but reordered -- the renderer interface must exist first so `pane-border-format` is just another surface backend, not a hardcoded conditional.

#### E.1 TmuxBorderSurface Implementation
- Set `pane-border-status top` per content pane
- Daemon dynamically updates `pane-border-format` via `set-option -p -t <pane>`
- Simplified content: pane title + group color indicator + activity badges
- Uses tmux format string syntax: `#[fg=color]text#[default]`

#### E.2 Two-Tier Header System
- **Desktop (>= 80 cols):** `TmuxBorderSurface` -- zero processes, zero pane slots, reclaims 1-3 rows per content pane
- **Phone (< 80 cols):** `TmuxPaneSurface` -- keeps BubbleTea headers with full touch interaction (hamburger, carousel, pane picker)
- Profile switch triggers surface swap (destroy old surface, create new one)

#### E.3 UX Regression Handling
Desktop `pane-border-format` cannot replicate the full rich header (lipgloss styles, clickable regions, resize arrows, AI indicators). Accepted tradeoffs:
- Desktop users have keyboard shortcuts for all header actions
- Context menus can be handled by daemon intercepting tmux mouse events via control mode
- If users want rich desktop headers, provide a config toggle to keep BubbleTea headers

Acceptance criteria:
- Desktop: zero header processes per window
- Desktop: pane title, group color, and activity badges visible in border
- Phone: full interactive headers unchanged
- Config option to force rich headers on desktop

---

### Phase F: Transient UI
**Goal:** Notifications, command palette, and overlays.
**Effort:** 1-2 weeks. **Risk:** Low.
**Depends on:** Phase D (popup lifecycle management).

#### F.1 Notification Toasts
New `cmd/tabby-notification` -- minimal BubbleTea app for `display-popup` with auto-dismiss. Shows: build completion, long-running command alerts, error toasts. Daemon manages lifecycle via Phase D's popup registry.

#### F.2 Pane Picker Popup
Implement the designed-but-unbuilt `cmd/tabby-pane-picker` from MOBILE_PR3. BubbleTea list UI in a `display-popup`. Accessible from:
- Pane-header center text tap (phone)
- Keyboard shortcut (desktop)
- Hamburger menu entry

#### F.3 Command Palette (stretch)
Fuzzy finder overlay for all tabby actions: switch window, switch group, toggle features, run commands. Similar to VS Code's Ctrl+Shift+P. Built on the popup pattern.

---

### Phase G: Overview Popup
**Goal:** Quick-navigation popup showing pane thumbnails in a grid.
**Effort:** 1-2 weeks. **Risk:** Low.
**Depends on:** Phase D (renderer interface).

Built as a `display-popup`, not a persistent view. Launched via keyboard shortcut or sidebar button.

#### G.1 Implementation
New `cmd/tabby-overview` -- BubbleTea app that:
- Captures pane content via `tmux capture-pane -t <pane> -p -e`
- Downscales using Unicode half-blocks (U+2580-U+259F) for 2x vertical density
- Renders a grid of thumbnails with command name + activity indicator
- Arrow keys to navigate, Enter to jump, Esc to dismiss
- Click a thumbnail to jump to that pane/window

Launched via `tmux display-popup -E -w 100% -h 100%`.

#### G.2 Scalability
- Practical limit: ~20 panes before capture overhead matters
- Beyond that: badge-only mode (command name + activity dot, no content preview)

#### G.3 Pane Capture Widget (stretch)
Separate from the overview popup: a sidebar widget that shows a live-ish snapshot of a pinned pane. Uses the same `capture-pane` + downscale approach, refreshed at ~1 fps. Useful for watching a build or log output while working in another pane.

---

### Phase R: Compositor Research
**Goal:** Validate whether tabby can own the terminal screen.
**Status: RESEARCH -- not committed until prototype proves feasibility.**
**Effort:** Unknown. **Risk:** Very high.

This phase exists because the compositor unlocks features that no other path can deliver: floating panes, custom borders, overlays that don't steal pane slots, non-rectangular layouts, smooth transitions. But it has fundamental unsolved problems.

#### R.1 The Core Problem
tmux redraws its content on every keypress, resize, and hook event. There is no tmux API to pause rendering or yield the screen. The compositor must either:
1. Run tmux headless via `tmux -C` and paint the screen itself (requires reimplementing a terminal emulator to parse PTY output)
2. Paint over tmux's output (gets overwritten on next tmux draw cycle, causing flicker)
3. Use terminal-specific overlay protocols (Kitty window layers, WezTerm overlay planes)

None of these are validated.

#### R.2 Prototype Requirements (1 week spike)
Build a 200-line throwaway prototype that attempts:
1. `tmux -C` event subscription + manual screen painting using CSI cursor positioning
2. `capture-pane` content + compositor rendering in a single full-screen pane
3. Terminal-specific overlay (Kitty graphics protocol layer)

**Decision gate:** If none produce stable, flicker-free output, the compositor is not viable on tmux. The feature set must be designed around tmux's actual capabilities (popups, border-format). This is an acceptable outcome -- Phases A through G already deliver substantial value.

#### R.3 MVP Compositor (if prototype succeeds)
If a viable mechanism is found, the minimum viable compositor is:
- Single overlay process that composites ONLY sidebar + headers + notifications
- Content panes remain native tmux panes
- Uses absolute cursor positioning to paint tabby chrome over tmux borders
- Passes input through to tmux for content panes
- Eliminates N+1 renderer processes (reduced to 1 compositor)

This does NOT take over content pane rendering. tmux still manages PTYs and draws pane content. The compositor only replaces tabby's own chrome.

#### R.4 Full Compositor (if MVP succeeds)
The further step beyond MVP -- tabby composites its own chrome AND can overlay additional UI:
- Floating notification panes
- Custom borders (rounded, gradient, thick)
- Inline overlays (tooltips, command palette without display-popup)

Content panes remain native tmux panes. The compositor handles tabby's UI layer only. This keeps complexity bounded -- we don't need to reimplement a terminal emulator or own PTY rendering.

This is a months-long effort and is explicitly NOT on a timeline.

#### R.5 Terminal Compatibility
The compositor requires testing across:
- iTerm2 (supports synchronized output)
- Terminal.app (limited CSI support)
- Blink on iOS (known limitations -- this is the phone use case)
- Alacritty, Kitty, WezTerm, Ghostty
- Nested tmux sessions

---

## Technical Prerequisites (apply across all phases)

### tmux Version Compatibility
Add `tmux -V` check to `tabby.tmux` Phase 1. Minimum supported: tmux 3.3+ (required for `display-popup`). Gate features behind version checks:
- `pane-border-format`: tmux 2.3+ (safe)
- `display-popup`: tmux 3.3+
- Control mode notifications: vary by version, parse defensively

### Coordinator Decomposition (Phase C)
Must happen before Phase D. Without it, every new feature compounds the 12.7K-line god object.

### State Persistence
Add lightweight state snapshot to `/tmp/tabby-state-{session}.json` written periodically, loaded on daemon restart. Covers client profiles, overview preferences, and popup state.

### Graceful Degradation
Every phase must degrade gracefully:
- Control mode fails -> fall back to USR1
- Compositor crashes -> tmux is still running underneath
- Overview capture-pane times out -> show stale thumbnails with age indicator
- Popup spawn fails -> log error, no crash

---

## Summary: What Ships When

| Phase | Delivers | Standalone Value? |
|-------|----------|-------------------|
| A: Stabilization | No sidebar flicker, no profile oscillation | YES -- most impactful user fix |
| B: Control Mode | Zero shell scripts, sync layout restore | YES -- perf + correctness |
| C: Decomposition | Maintainable codebase | Foundational (no user-visible change) |
| D: Renderer Interface | Multiple surface backends | Foundational (enables E, F, G) |
| E: Header Elimination | Desktop: reclaim rows, zero header processes | YES -- visible perf improvement |
| F: Transient UI | Notifications, pane picker, command palette | YES -- high polish |
| G: Overview Popup | Grid of pane thumbnails for quick navigation | YES -- new capability |
| R: Compositor | Floating panes, custom borders, overlays | IF VALIDATED -- transforms the project |

**The 80/20 path** (maximum value, minimum risk): Phases A + F.2 (pane picker) + G (overview popup). These three deliver the most user-visible value with the least infrastructure. Everything else is valuable but not urgent.
