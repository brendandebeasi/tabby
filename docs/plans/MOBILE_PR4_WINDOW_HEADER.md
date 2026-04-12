# Mobile PR 4: Window-Level Header + Per-Pane Title Strips

> **Revised from initial plan**: The original plan replaced per-pane headers entirely with a
> single window-header. After implementation, the user directed a pivot: keep per-pane headers
> as 1-row title strips AND add the window-header as a separate control bar. Both coexist.
> The phone button bar (hamburger/prev/close/next) lives exclusively on the window-header.
> Per-pane headers are always 1 row on both desktop and phone — they show title/cmd only.

## Goal

Add a **window-header pane** at the top of each window (full content width, 1-3 rows) that
carries the 4 navigation controls on phone. Simultaneously keep the existing **per-pane
pane-header** panes (1 row each, above each content pane) as plain title strips with no
buttons.

## User-facing result

Layout per window:

```
sidebar | window-header (1 or 3 rows, full width)
        | pane-header   (1 row, above content pane 1)
        | content pane 1
        | pane-header   (1 row, above content pane 2)
        | content pane 2
```

- **window-header** (desktop, >=60 cols): 1 row. Shows `{idx}:{name} · {active pane title}`. No buttons.
- **window-header** (phone, <60 cols): 3 rows. Middle row: `[≡ ] [◄ ] [✕ ] [► ]  title`.
  Rows 0 and 2 are blank padding for fat touch targets.
- **pane-header**: always 1 row (2 with `CustomBorder`). Shows pane index, command/title, path,
  and action buttons (split/close/resize). No hamburger or carousel buttons — those moved to
  window-header. This is unchanged from the original desktop behavior.

Button actions on window-header:
- hamburger: toggle sidebar collapse
- prev/next: `tmux previous-window` / `tmux next-window`
- close: `tmux kill-window`

## Design

### 1. Both header types coexist

The daemon now spawns per window: 1 sidebar-renderer + 1 window-header + N pane-headers
(one per content pane). All are separate tmux panes.

- `spawnWindowHeaders()`: spawns one `window-header` pane topmost per window.
- `spawnPaneHeaders()`: spawns one `pane-header` pane above each content pane.
- Both are called from `doPaneLayoutOps()` under `@tabby_spawning=1`.

### 2. Client ID naming

- sidebar: `@<window_id>` (unchanged)
- window-header: `window-header:@<window_id>`
- pane-header: `header:%<pane_id>`

### 3. Height sync

`RunHeaderHeightSync` handles both types in one pass, using the correct function per type:
- window-header: `desiredWindowHeaderHeightForWidth(windowWidth)` -> 1 (desktop) or 3 (phone)
- pane-header: `desiredPaneHeaderHeight()` -> always 1 (or 2 with CustomBorder)

### 4. Rendering

- `RenderHeaderForClient(clientID, ...)`: handles `window-header:@<id>` clients. Phone layout
  with 4 buttons. Desktop layout with window+pane title only.
- `RenderPaneHeaderForClient(clientID, ...)`: handles `header:%<id>` clients. Always 1-row title
  strip. No phone button layout. Same desktop rendering as before the pivot.

### 5. Click regions

window-header phone layout:
| action | col range |
|---|---|
| `window_header:hamburger` | 0..3, rows 0-2 |
| `window_header:prev_window` | 4..7, rows 0-2 |
| `window_header:close_window` | 8..11, rows 0-2 |
| `window_header:next_window` | 12..15, rows 0-2 |
| `window_header:menu` | 16..width, rows 0-2 |

pane-header: unchanged from original (split, collapse, resize, close buttons on row 0).
No carousel or hamburger buttons on pane-header.

### 6. Action handlers

Four new cases in HandleInput:
- `window_header:hamburger` -> sidebar collapse toggle
- `window_header:prev_window` -> `tmux previous-window` (1500ms timeout)
- `window_header:next_window` -> `tmux next-window` (1500ms timeout)
- `window_header:close_window` -> `tmux kill-window` (1500ms timeout)
- `window_header:menu` -> existing `header_context` handler

## Files touched

- `cmd/window-header/main.go` (new binary, added in this PR)
- `cmd/pane-header/main.go` (restored; was briefly deleted during initial implementation)
- `cmd/tabby-daemon/main.go`
  - `getPaneHeaderBin()`, `getWindowHeaderBin()` — both present
  - `spawnPaneHeaders()` + `paneTargetRegex` / `paneTargetFromStartCmd()` — restored
  - `spawnWindowHeaders()` — new
  - `doPaneLayoutOps()` — calls both spawn functions
  - `cleanupOrphanedHeaders()` — handles both window-header (dedup by window) and
    pane-header (dedup by target pane) separately
  - `syncClientSizesFromTmux()` — tracks both `window-header:@<id>` and `header:%<id>` clients
  - Height anomaly enforcement per type
- `cmd/tabby-daemon/coordinator.go`
  - `desiredPaneHeaderHeight()` — always 1 (or 2); never 3 on phone
  - `desiredWindowHeaderHeight()` / `desiredWindowHeaderHeightForWidth()` — 1 or 3
  - `RunHeaderHeightSync()` — routes to correct function per header type
  - `RenderHeaderForClient()` — window-header renderer (phone buttons here)
  - `RenderPaneHeaderForClient()` — pane-header renderer (title strip, no phone buttons)
  - `header:` prefix guards added alongside `window-header:` guards throughout

## Testing

- Unit: `go test ./cmd/tabby-daemon/... ./cmd/pane-header/... ./cmd/window-header/...` passes.
- Manual (phone via Blink at <60 cols):
  - Each window has one 3-row window-header at top with 4 buttons.
  - Each content pane has one 1-row pane-header above it (title strip, no buttons).
  - Tap prev/next on window-header: window switches.
  - Tap hamburger: sidebar collapses/expands.
  - Tap close: window closes.
- Manual (desktop >=60 cols):
  - Each window has one 1-row window-header at top (title only, no buttons).
  - Each content pane has one 1-row pane-header above it (with split/close/resize buttons).
- Regression: resize stability fixes from `ae9c3a9` (phone threshold 60, desktop-preference
  override, RunWidthSync timeout, renderer debounce) remain intact.

## Out of scope

- Per-pane border status as additional identity on desktop multi-pane windows.
- Swipe gestures, long-press, or animation.
- Color theming beyond existing header styles.
