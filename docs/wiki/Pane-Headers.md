[Home](Home.md) › Pane Headers

# Pane Headers

Each content pane can carry a one-row header with its title and a set of
clickable controls. These are overlay panes drawn by Tabby, not tmux's native
border titles, which is what makes the buttons work.

<p align="center">
  <b>Pane headers</b><br/>Titles that survive vertical and horizontal splits.<br/>
  <img src="../../screenshots/vertical_horizontal_panes.png" width="90%" alt="Headers on vertical and horizontal pane splits"/>
</p>

Turn them on:

```yaml
sidebar:
  pane_headers: true
```

## Appearance

```yaml
pane_header:
  active_fg: "#ffffff"
  inactive_fg: "#ffffff"
  terminal_bg: "#faf4ed"
  border_lines: heavy
  custom_border: true
```

`terminal_bg` has to match your terminal's real background colour. Tabby cannot
query it, and headers blend against it. A mismatch shows as a band of the wrong
colour along the header edge.

`border_lines` accepts the tmux values: `single`, `double`, `heavy`, `simple`,
`number`. `custom_border` lets Tabby draw the border itself rather than
deferring to tmux, which is required for the drag and resize controls.

## Border colour from the tab

```yaml
pane_header:
  border_from_tab: true
  auto_border: false
```

`border_from_tab` paints the active pane's border in the window's group or
custom colour, so a red Frontend window has a red border. `auto_border` pushes
the same colour into tmux's global `pane-border-style`, keeping it consistent
when the sidebar is collapsed.

## Dimming inactive panes

```yaml
pane_header:
  dim_inactive: true
  dim_opacity: 0.6
```

Inactive panes are blended toward `terminal_bg` at that opacity. `0.0` is fully
dimmed, `1.0` is no dimming. Somewhere between `0.5` and `0.7` reads well on
most themes. This needs `terminal_bg` to be correct.

To re-apply dimming without changing focus, for instance from a script:

```bash
tabby cycle-pane --dim-only
```

## Controls

```yaml
pane_header:
  handle_icon: "..."
  draggable: true
  collapse_expanded_icon: "▾"
  collapse_collapsed_icon: "▸"
  resize_horizontal_grow_icon: ">"
  resize_horizontal_shrink_icon: "<"
  resize_vertical_grow_icon: "↓"
  resize_vertical_shrink_icon: "↑"
  resize_separator: "¦"
```

`handle_icon` is the drag grip. With `draggable: true` you can drag a pane by
its header to rearrange the layout. The collapse icons roll a pane up to just
its header and back. The resize icons grow and shrink the pane in each axis, and
`resize_separator` is drawn between the pairs.

Set any icon to an empty string to hide that control.

## Titles

A pane's title tracks its running command by default. To pin one:

- Right click the pane in the sidebar and pick Rename.
- Or set it directly:

```bash
tmux set-option -p -t %123 @tabby_pane_title "log tail"
```

A pinned title stays until you clear it:

```bash
tmux set-option -p -t %123 -u @tabby_pane_title
```

Right click the pane and pick Unlock pane name for the same result.

Window names work the same way. `prefix + r`, or right click and Rename, sets
`@tabby_name_locked 1` so automatic naming stops overwriting it.

## Pane layout continuity

Splitting and closing panes normally disturbs the ratios of the panes that
remain. Tabby restores them through the `preserve-pane-ratios` hook, wired up
automatically. The smart kill path is bound to `prefix + x`, which closes the
pane and puts the surviving layout back the way it was.

## When headers get in the way

On a very short pane the header costs a row you may want. Turn them off for the
whole session:

```yaml
sidebar:
  pane_headers: false
```

tmux's native pane borders take over, and `border_from_tab` still colours them.
