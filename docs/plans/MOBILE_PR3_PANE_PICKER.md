# Mobile PR 3: Pane Picker Popup

## Goal

For phone clients with many panes in a window, cycling one-by-one with the prev/next arrows from PR 2 gets tedious. This PR adds a jump-to-any-pane picker: tapping the pane count text in pane-header row 2 opens a full-screen popup listing every pane in the current window with its command and cwd. Selecting one makes it active (and zoomed, since zoom is in effect).

Polish, not essential. Ship PR 1 and PR 2 first. This exists so the path from "the prev/next arrow UX is fine" to "I need a picker" is already designed when the user hits 6+ panes in a window and the arrows start feeling slow.

## User-facing result

- On a phone client, in the pane-header row 2, the center text `pane 2/4 . zsh` is now a tappable region.
- Tapping it opens a full-screen tmux popup listing every pane in the current window:
  ```
  > 1  zsh          ~/git/tabby
    2  nvim         ~/git/tabby/cmd/tabby-daemon
    3  go test      ~/git/tabby/pkg/daemon
    4  tail -f log  ~/tmp
  ```
- Up/down arrows move the cursor; Enter selects; Esc dismisses.
- Selecting a pane closes the popup, runs `tmux select-pane -t <pane_id>`, and the zoom transfers automatically to the newly-selected pane.
- On a desktop client, the center text is not tappable (or the tap is a no-op). Desktop users have tmux's native pane navigation and don't need the picker.

## Design

### 1. New bubblezone region

In the pane-header renderer on phone profile, register a new region `pane_header:pane_picker` covering the center text area of row 2 (between the prev and next arrow regions). When `pane_count > 1`, the region is active; when `pane_count == 1`, it's disabled (same as the arrow regions).

### 2. Widget action handler

New handler in the coordinator: `pane_header:pane_picker` -> spawn
```
tmux display-popup -E -w 100% -h 100% -- <pane-picker command>
```

The command is a new small binary or script that:

1. Reads the current window ID from tmux env (`TMUX_PANE` -> derive window).
2. Queries `tmux list-panes -t <window_id> -F '#{pane_id}|#{pane_index}|#{pane_current_command}|#{pane_current_path}'`.
3. Renders a list UI with the current pane highlighted.
4. Reads keyboard input: up/down/enter/esc.
5. On enter: runs `tmux select-pane -t <chosen_pane_id>` then exits (which closes the popup because of `-E`).
6. On esc: exits without selection.

### 3. Implementation choices

Two paths, pick based on appetite:

- **(a) Tiny standalone Go binary `cmd/tabby-pane-picker`.** Self-contained, uses Bubble Tea (already a dependency) for the list UI. ~150 lines. Runs only when the popup is opened, so cold-start latency matters — aim for <100ms from click to visible list.
- **(b) Shell script using `fzf`.** Even smaller. `tmux list-panes | fzf --preview ... | xargs tmux select-pane -t`. Fast, no new Go code, but adds an fzf dependency and the UX is fzf's default which may not match the tabby look.

Recommendation: **(a)**. Consistency with the rest of tabby's rendering, no external dependencies, matches color theming, easy to extend later (e.g. show pane size, last-activity time, or a mini preview).

### 4. Interaction with zoom

- If the window is currently zoomed (which on a phone client it always is per PR 2), `select-pane` transfers zoom to the new pane. Nothing to do.
- If somehow the window is not zoomed when the picker is used, the picker still selects the pane correctly; the phone client's next active-client event will auto-zoom the newly-selected pane per PR 2's `RunZoomSync` logic.

## Files touched

- `cmd/tabby-daemon/coordinator.go`
  - Add widget action handler for `pane_header:pane_picker`.
- `cmd/pane-header/` (renderer)
  - Register `pane_header:pane_picker` bubblezone region on the center text of row 2.
  - Dim the region when `pane_count == 1`.
- `cmd/tabby-pane-picker/` (new binary, option (a)) OR `scripts/tabby-pane-picker.sh` (option (b)).
- `scripts/tabby-pane-picker.sh` wrapper invoked by `display-popup` (even with option (a), a thin shell wrapper keeps the popup command string simple).

## Testing

- Manual: phone client on a window with 6 panes. Tap the pane count text. Confirm popup opens within 150ms and lists all 6 panes with their command and cwd.
- Manual: arrow down to pane 4, press Enter. Confirm popup closes and pane 4 becomes active and zoomed.
- Manual: open picker, press Esc. Confirm popup closes and the previously-active pane is unchanged.
- Manual: on a single-pane window, confirm the pane count text is either not tappable or tapping is a no-op.
- Regression: PR 1 hamburger and PR 2 prev/next arrows still work.

## Out of scope for this PR

- Filtering / search in the picker. `fzf` would give this for free, but option (a) means typing filters the list by command-name prefix, which is nice-to-have, not essential.
- Mini preview pane showing the target pane's last 10 lines of output. Cute, but adds enough complexity that it should be its own follow-up.
- Cross-window pane jump (picker shows panes from other windows too). Out of scope — that's a different feature, closer to a global fuzzy pane finder.

## Open questions before building

1. Option (a) vs (b). Default (a).
2. Filtering by typing: include in initial implementation or follow-up? Leaning include — it's a few extra lines in Bubble Tea and makes the picker feel much faster when you have >5 panes.
3. Should the picker also be accessible from the hamburger popup? i.e. the sidebar popup could have a "jump to pane" entry that opens the picker. Probably yes, but that's wiring that belongs to this PR since the picker is otherwise only reachable from pane-header row 2.
