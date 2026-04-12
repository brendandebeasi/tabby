# Mobile PR 1: Client Profile + Pane-Header Hamburger

## Goal

Make tabby usable on narrow mobile clients (primarily Blink on iOS) by:

1. Classifying each connected client as `desktop` or `phone` based on terminal width.
2. Giving phone-profile clients a fat-tappable hamburger button in the top-left of every content pane's header, which opens the full sidebar as a tmux popup overlay.

This PR does **not** change pane zoom behavior or add the pane carousel — those land in PR 2. Scope here is purely: "a phone user can see and tap a hamburger that opens the sidebar."

## User-facing result

- On a client narrower than 80 cols, the pane-header above every content pane grows from 1 row to 2 rows. The leftmost 2 cols x 2 rows of that header is a clickable hamburger glyph block.
- Tapping the hamburger opens a full-screen tmux popup hosting the sidebar renderer. Esc dismisses.
- On a desktop client (>= 80 cols), pane-header stays 1 row and there is no hamburger. No regression.
- If a desktop and a phone are both attached simultaneously and focus moves between them, pane-header on the currently-viewed window resizes between 1 row and 2 rows as the active client's profile changes. Passive viewers briefly see the other client's layout — same tradeoff already accepted for auto-zoom planning.

## Design

### 1. Client profile detection

- New field on `Coordinator`:
  ```go
  clientProfile   map[string]string // clientID -> "desktop" | "phone"
  clientProfileMu sync.RWMutex
  ```
- Threshold: width < 80 cols = `phone`, else `desktop`. Single tier for now; no `compact` middle band until there is a real use case.
- Recompute points:
  - `client-attached` hook path (new client joining)
  - `client-resized` hook path (client terminal resize)
  - `client-active` / `client-focus-in` hook path (focus change between already-attached clients)
  - `clientGeometryTicker` in `main.go` (self-healing fallback)
- The existing `clientWidths` map already carries the width at the right moments; profile detection is a thin wrapper on top of the existing size snapshot plumbing.
- No config override knob in this PR. Pure width-based. Blink and other clients all use the same math.

### 2. Pane-header height becomes profile-aware

- Today: pane-header height is a hardcoded `"1"` passed to `tmux split-window -l 1` in `cmd/tabby-daemon/main.go:830`.
- Also today: `coordinator.go:346` already has a `headerHeight := 1; if CustomBorder { headerHeight = 2 }` path, so the renderer already handles a 2-row variant.
- Change: introduce a single helper
  ```go
  func (c *Coordinator) desiredPaneHeaderHeight() int
  ```
  which returns 2 if **the currently active client** has profile `phone` **or** `config.PaneHeader.CustomBorder` is true, else 1.
- Replace both the `main.go:830` hardcoded spawn value and the `coordinator.go:346` inline check with calls to this helper.
- Add `RunHeaderHeightSync(activeClientID)`: iterates pane-header panes on the active window and issues `tmux resize-pane -t <pane-header-pid> -y N` where N is the helper's current return value. Runs only when the new target differs from the current height — idempotent, matches the `RunWidthSync` pattern exactly.
- Hook `RunHeaderHeightSync` into the same paths that currently call `RunWidthSync`: `client-active` transitions and the geometry ticker. No new hook plumbing, just an extra call alongside the existing width sync.
- Fix the spawn-time issue too: when `spawnRenderersForNewWindows` creates a new pane-header, it reads from `desiredPaneHeaderHeight()` rather than a literal `"1"`, so new panes born into a phone-active session start at the correct height.

### 3. Hamburger click region

- Renderer side (`cmd/pane-header/`):
  - When the header is 2 rows tall AND the active client's profile is `phone`, render a hamburger glyph block in the top-left 2 cols x 2 rows. Suggested content:
    ```
    =
    =
    ```
    (Two horizontal bar glyphs stacked — readable at small sizes, clearly a tap target. Refine later.)
  - Register a bubblezone region named `pane_header:hamburger` covering exactly those 4 cells.
  - Right of the hamburger, row 1 renders the existing pane-name/cwd content. Row 2 is reserved for the carousel controls in PR 2 — leave empty (but present) in this PR so the layout doesn't have to change again.
- Coordinator side:
  - Add a new widget action handler for `pane_header:hamburger`: when clicked, spawn
    ```
    tmux display-popup -E -w 100% -h 100% -- <tabby-sidebar-popup command>
    ```
    The popup runs as a detached shell command; tabby does not block on it.

### 4. Sidebar popup host

Two implementation options, pick one before building:

- **(a) New binary `cmd/tabby-sidebar-popup`:** small Go program that opens the existing daemon Unix socket, subscribes to render frames like sidebar-renderer does, forwards stdin to the daemon as input events, and exits cleanly on Esc. Clean separation. ~200 lines. Keeps sidebar-renderer uncluttered.
- **(b) Reuse `cmd/sidebar-renderer` with a `--popup` flag:** adds a branch in the existing renderer that skips the tmux-pane-attachment startup code and runs against the popup's PTY instead. Less new code but pollutes the renderer with popup-mode conditionals.

Recommendation: **(a)**. The renderer has enough going on already; a popup shim is cheap to write and easy to kill if the design changes.

Either way, the popup needs to render "fast enough that it feels instant." Target <50ms from click to first frame. The daemon is already running and has the render state hot, so the only cost is subprocess spawn + one socket round-trip + first frame draw. Should be achievable.

## Files touched

- `cmd/tabby-daemon/coordinator.go`
  - Add `clientProfile` map and mutex.
  - Add `SetClientProfile`, `GetClientProfile`, `ActiveClientProfile` accessors.
  - Add `desiredPaneHeaderHeight()` helper.
  - Replace inline `headerHeight` math with helper calls.
  - Add `RunHeaderHeightSync(activeClientID)`.
  - Add hamburger widget action handler and bubblezone region registration.
- `cmd/tabby-daemon/main.go`
  - Replace hardcoded `"1"` spawn height with `desiredPaneHeaderHeight()` call.
  - Add `RunHeaderHeightSync` calls alongside existing `RunWidthSync` calls in the `client-active` / geometry-tick paths.
  - Recompute client profile on attach/resize/active/geometry-tick.
- `cmd/pane-header/` (renderer)
  - Accept 2-row layout.
  - Render hamburger glyph block in top-left 2x2 when profile is phone.
  - Register `pane_header:hamburger` bubblezone region.
- `cmd/tabby-sidebar-popup/` (new binary, option (a)) OR `cmd/sidebar-renderer/main.go` (`--popup` branch, option (b)).
- `scripts/tabby-sidebar-popup.sh` (new, optional wrapper around the binary — keeps the `display-popup` command string simple).
- Possibly `pkg/daemon/protocol.go` if the popup needs a new message type for "one-shot render + input forward" — or reuse the existing subscribe flow.

## Testing

- Manual: attach Blink at narrow width, confirm pane-header is 2 rows and hamburger is visible and clickable.
- Manual: attach desktop client at >= 80 cols, confirm pane-header is 1 row and no hamburger.
- Manual: attach both simultaneously, switch focus between them, confirm pane-header resizes on focus change without flicker in the active client and without layout corruption.
- Manual: tap hamburger, confirm sidebar popup opens full-screen within 100ms, renders live, responds to input, dismisses on Esc.
- Regression: desktop-only workflows unchanged. Existing `CustomBorder` config still produces a 2-row header.

## Out of scope for this PR

- Auto-zoom of active pane on phone profile (PR 2).
- Pane carousel next/prev controls (PR 2).
- Pane picker popup (PR 3).
- `@tabby_client_profile` explicit override (future, if width heuristic proves insufficient).
- Per-window zoomOwner tracking (PR 2).

## Open questions before building

1. Option (a) vs (b) for the sidebar popup host. Default is (a).
2. Hamburger glyph choice — `=` stacked, or Unicode bars, or `[M]`. Needs a 5-minute visual check on Blink at real phone sizes.
3. Is there any existing desktop workflow that relies on pane-header being exactly 1 row that the 2-row-on-focus-transition behavior would break? Need to grep for pane-header height assumptions outside the coordinator.
