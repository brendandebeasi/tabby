# Custom pane borders

Tabby can draw its own pane borders instead of tmux's native ones: a full
rounded box around single-pane windows with a labeled top edge, split/close
buttons, gradients on every edge, active/inactive dimming, drag-to-resize, and
an experimental sixel graphics strip. Multi-pane windows degrade to a labeled
top edge per pane.

```
╭─ mycode ───────────────────── | - x ╮   rounded corners, dir label,
│                                     │   split-h / split-v / close buttons
│            your shell               │   gradient on all four edges
│                                     │   active pane vivid, inactive dimmed
╰─────────────────────────────────────╯
```

## Enable it

The custom chrome is on by default (`pane_header.native: false`), but the FULL
box needs all four edges. In `config.yaml`:

```yaml
pane_header:
    native: false                 # tabby-drawn chrome (default)
    dim_inactive: true            # dim inactive panes' boxes
    border:
        style: rounded            # rounded | single | double | heavy
        edges: [top, bottom, left, right]   # all four = full box
```

With only `edges: [top]` (the default) you get a single top edge, not a box.

## Runtime toggles (tmux options)

These flip live without a config reload (read uncached on the next refresh):

| Option | Values | Effect |
|--------|--------|--------|
| `@tabby_custom_borders` | `on` / `off` | Force the box on, or fall back to native borders. Overrides config. |
| `@tabby_border_graphics` | `sixel` / `kitty` / `off` | Replace the top edge with a gradient image in the chosen protocol (see below). |
| `@tabby_border_sixel` | `on` / `off` | Legacy alias for `@tabby_border_graphics sixel`. |
| `@tabby_border_enable` (pane-local) | `0` | Opt a single pane out of the box. Set with `set-option -p`. |

Example:

```sh
tmux set-option -g @tabby_custom_borders on
tmux set-option -p @tabby_border_enable 0   # this pane only, no box
```

A refresh (any layout change, or `scripts/signal-daemon.sh`) applies the change.

## Sixel / kitty graphics (experimental)

`@tabby_border_graphics sixel` (or `kitty`) makes the top edge emit a gradient
IMAGE instead of the glyph bar, in the chosen protocol. Both are generated in Go
(no external encoder) and wrapped in tmux passthrough, so they need:

```sh
tmux set-option -g allow-passthrough on
```

Whether pixels actually render depends on the ATTACHED terminal:

| Protocol | Terminals that render it |
|----------|--------------------------|
| `sixel`  | iTerm2, Ghostty, WezTerm |
| `kitty`  | kitty, Ghostty, WezTerm  |

**IMPORTANT: this does NOT work over mosh** — mosh strips both sixel and kitty
graphics. Attach from a local terminal (or plain ssh) that supports the protocol.
If the terminal can't show it you'll see a blank top edge; toggle it back off.
The generators are unit-tested and the sixel is verified decodable to a correct
gradient PNG.

## Trying it on the dev VM

`scripts/vm-demo.sh` brings up a demo session on tabby-dev and toggles both
features live. Run it through orb:

```sh
orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh up'          # start (box on)
orb -m tabby-dev tmux -L tbdemo attach -t demo                 # attach to see it
orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh borders off' # native borders
orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh sixel on'    # graphics strip
orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh status'
orb -m tabby-dev bash -lc '.../scripts/vm-demo.sh down'
```

(`.../` = the worktree path `/.../.claude/worktrees/custom-pane-borders`.)

## How it works

tmux always draws a 1-cell separator between panes that can only be styled, not
rendered into. So a tabby box is: aux renderer panes drawing the glyphs (one per
edge) plus the tmux seams styled to the terminal background so they vanish.
Single content pane -> full box (top/bottom bars own the rounded corners, two
1-col side panes). Multiple panes -> a labeled top edge per pane (full per-pane
boxes would need up to 4N border panes with colliding seams).

Edges are re-pinned to 1 cell on every layout pass because any window/sidebar
resize makes tmux redistribute the box's panes proportionally, widening them.

Drag-to-resize: dragging a pane's top edge resizes it against its stacked
neighbor (`resize-pane -y`); the change persists because the repin only touches
border panes. Native borders remain available as the `off` fallback.
