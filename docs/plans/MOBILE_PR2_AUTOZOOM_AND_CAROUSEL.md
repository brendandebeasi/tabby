# Mobile PR 2: Auto-Zoom + Pane Carousel

## Goal

Make multi-pane windows navigable on phone clients by collapsing all non-active panes via tmux zoom, and giving the user fat-tappable prev/next controls in the pane-header row 2 to cycle through the hidden panes.

Builds directly on PR 1, which established client profiles, the 2-row pane-header, and the hamburger. This PR fills row 2 of the pane-header with navigation and adds the coordinator logic to manage zoom state.

## User-facing result

- When a phone-profile client becomes active on a window with multiple panes, tabby automatically zooms the currently-active pane so it occupies the entire window area. The other panes are hidden but not destroyed — their shells keep running, output keeps buffering.
- The phone user sees `pane 2/4 . zsh` in the pane-header row 2, flanked by `[<]` and `[>]` controls. Tapping the controls cycles to the previous or next pane, which becomes the new active-and-zoomed pane.
- When focus returns to a desktop client, tabby automatically unzooms the window (if it was tabby that zoomed it), restoring the full tiled layout.
- Manual zoom from a desktop user (pressing prefix+z explicitly) is NOT unzoomed by tabby. Only zooms that tabby initiated are reversed.

## Design

### 1. Auto-zoom on phone active

- Trigger point: the same `client-active` / `client-focus-in` / geometry-tick path that currently calls `RunWidthSync` and (from PR 1) `RunHeaderHeightSync`.
- Logic: when the newly-active client has profile `phone` and is now the "driver" for its window:
  - Query the window's current zoom state via `tmux display-message -p -t <window_id> '#{window_zoomed_flag}'`.
  - If not zoomed, issue `tmux resize-pane -Z -t <active_pane_id>`.
  - Mark the window as `zoomOwner = phone` in the Coordinator.
- Zoom transfers automatically when `select-pane` moves to a different pane in a zoomed window — tmux handles this. So the carousel's next/prev buttons do not need to explicitly re-zoom; they just call `select-pane` and tmux keeps the zoom active against the new selection.

### 2. Auto-unzoom on desktop return

- When a client with profile `desktop` becomes active on a window where the Coordinator recorded `zoomOwner == phone`:
  - Issue `tmux resize-pane -Z -t <active_pane_id>` (toggles zoom off).
  - Clear `zoomOwner`.
- Important: if a desktop user manually zoomed with prefix+z, `zoomOwner` stays empty (tabby never set it), so tabby will not unzoom their manual zoom. Respect user intent.
- If the window's zoom flag is off but `zoomOwner == phone` (e.g. the user unzoomed manually from the phone), treat that as a clean state and clear `zoomOwner`.

### 3. zoomOwner tracking

- New map on Coordinator:
  ```go
  windowZoomOwner   map[string]string // windowID -> "phone" | ""
  windowZoomOwnerMu sync.RWMutex
  ```
- Populated only when tabby itself issues a zoom. Cleared when tabby unzoom, when the window's zoom flag drops unexpectedly, or when all clients with phone profile have disconnected from the window.
- Periodic sanity-check in the geometry ticker: if a window has `zoomOwner == phone` but no phone client is currently attached to the session, unzoom and clear.

### 4. Pane carousel in pane-header row 2

Row 2 layout on phone profile (width-constrained, so controls need to be compact but still fat-tappable — minimum 2 cells per tap target):

```
[ < ]  2/4 . zsh          [ > ]
```

- `[ < ]`: bubblezone region `pane_header:prev_pane`. Minimum 3 cols x 1 row. Click action: `tmux select-pane -t :.-`.
- Center text: pane index + total + command name. Read-only. Tapping it is reserved for PR 3's pane picker popup — in this PR, tapping the center is a no-op (or optionally also opens the hamburger, TBD).
- `[ > ]`: bubblezone region `pane_header:next_pane`. Minimum 3 cols x 1 row. Click action: `tmux select-pane -t :.+`.
- Spacing: left-align prev, right-align next, center the index text. Works at any pane width because the center text is sacrificial — it truncates before the tap targets do.
- All three regions live on row 2 of the 2-row pane-header. Row 1 is unchanged from PR 1 (hamburger on the left, pane name/cwd right of it).

### 5. Widget action handlers

Two new handlers in the coordinator's widget action switch:

- `pane_header:prev_pane` -> run `tmux select-pane -t :.-` on the active window.
- `pane_header:next_pane` -> run `tmux select-pane -t :.+` on the active window.

After the select-pane call, the Coordinator's existing pane-activity tracking picks up the new active pane and re-renders all pane-headers. Zoom transfer is implicit via tmux.

### 6. Edge cases

- **Window with only one pane:** no point in zooming or showing prev/next. On phone profile with pane_count == 1, pane-header row 2 shows `1/1 . zsh` with no arrow regions (or regions are dim and disabled). Zoom is also skipped.
- **Pane spawn/kill while zoomed:** tmux handles pane count changes under zoom correctly. The Coordinator just needs to re-render pane-headers so the `N/M` count updates.
- **Multiple phone clients attached:** first one to take focus owns the zoom. Second phone taking focus is a no-op (window is already zoomed). Tracking `zoomOwner` as a simple tag (not a client ID) is sufficient.
- **Phone disconnects while window is zoomed:** geometry ticker sweeps, sees no phone client, unzooms.
- **Phone switches windows:** the previous window keeps its zoom state; the new window gets its own zoom applied. zoomOwner is per-window.

## Files touched

- `cmd/tabby-daemon/coordinator.go`
  - Add `windowZoomOwner` map and mutex.
  - Add `RunZoomSync(activeClientID)` method called alongside `RunWidthSync` / `RunHeaderHeightSync`.
  - Add widget action handlers for `pane_header:prev_pane` and `pane_header:next_pane`.
  - Add periodic zoom-state sanity check in the geometry tick path.
- `cmd/tabby-daemon/main.go`
  - Call `RunZoomSync` from the existing `client-active` and geometry-tick paths.
- `cmd/pane-header/` (renderer)
  - Render row 2 with prev/center/next layout on phone profile.
  - Register `pane_header:prev_pane` and `pane_header:next_pane` bubblezone regions.
  - Dim or hide arrow regions when pane count == 1.
  - Show pane index + total + command name in the center.

## Testing

- Manual: phone client active on a 4-pane window. Confirm auto-zoom to active pane within 100ms of focus.
- Manual: tap `[>]`. Confirm next pane becomes active and stays zoomed. Tap `[<]`. Confirm previous pane becomes active.
- Manual: switch focus to desktop client. Confirm auto-unzoom within 100ms. Switch back to phone, confirm auto-re-zoom.
- Manual: on desktop client, press prefix+z to manually zoom. Switch to phone, confirm tabby does NOT unzoom (respects manual zoom). But also confirm phone's auto-zoom machinery doesn't zoom again (already zoomed).
- Manual: on phone, press prefix+z to manually unzoom. Confirm `zoomOwner` clears and tabby does not immediately re-zoom.
- Manual: phone disconnects while zoomed. Confirm geometry ticker unzooms within one tick.
- Regression: desktop-only workflows unchanged. PR 1 hamburger still works.

## Out of scope for this PR

- Pane picker popup (tap center text) — PR 3.
- Gestures (swipe-to-cycle) — out of scope entirely; tmux does not receive gesture events, only click events on bubblezone regions.
- Visual transition animations between panes.

## Open questions before building

1. Minimum arrow target size. 3 cols x 1 row is the minimum that's clearly a button. Can go 4 or 5 cols x 1 row if there's space. Need to see it on a real phone.
2. Arrow glyphs: `[<]` / `[>]`, or `<<` / `>>`, or Unicode arrows. Unicode is prettier but renders inconsistently across terminals; brackets are universal.
3. Should the center text tap open a pane picker in this PR, or leave that for PR 3? I lean PR 3 — keeps this PR focused.
