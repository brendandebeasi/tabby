[Home](Home.md) › Sidebar

# Sidebar

The sidebar is a real tmux pane running a Bubble Tea renderer, one per window.
That is what makes it clickable, where tmux's own status line is not.

## Position and mode

```bash
tmux set -g @tabby_sidebar_position right   # left (default) or right
tmux set -g @tabby_sidebar_mode partial     # full (default) or partial
```

Or in `config.yaml`:

```yaml
sidebar:
  position: left
  mode: full
```

`full` spans the whole window height regardless of how the content is split.
`partial` attaches to the current pane only and respects existing vertical
splits, which suits a layout where you want the sidebar next to one pane rather
than the whole window.

Both changes need the sidebar toggled off and on to take effect: press
`prefix + Tab` twice.

## Width

```bash
tmux set -g @tabby_sidebar_width 25
```

Drag the right border to resize. The new width propagates to every other window
at the same [profile](Responsive-Layout.md) on the next tick, and is written
back to `@tabby_sidebar_width_<profile>` so it survives a daemon restart.

Per-profile defaults:

```yaml
sidebar:
  width_mobile: 15
  width_tablet: 20
  width_desktop: 25
```

## Collapsing

Collapse stashes the sidebar pane into a hidden holding window using tmux
`break-pane`, and restores it with `join-pane`. The renderer process keeps
running the whole time, so coming back is immediate rather than a fresh start.

Three ways to trigger it:

- `Cmd+Shift+\`, once your terminal is
  [configured to send it](Keyboard-and-Mouse.md#sending-cmdshift-to-tmux)
- the `≡` button in the phone window header
- `tabby hook toggle-collapse-sidebar` from any shell

Clicking the sidebar's right edge collapses it too.

This is not the same as `prefix + Tab`, which stops the daemon and unhooks
Tabby from the session entirely.

## Chrome

```yaml
sidebar:
  header:
    fg: "auto"
    bg: "auto"
    active_color: true
  new_tab_button: false
  new_group_button: false
  close_button: false
  show_empty_groups: false
  line_height: 0
```

`auto` on the header colours picks from the active theme. `active_color: true`
tints the header with the active window's group colour, so the top of the
sidebar tells you where you are at a glance.

The banner itself is `text` (default `TABBY`), `height` in rows (default `3`),
and `padding_bottom`, the blank rows under it (default `1`). Set `height: 0` to
drop the banner and start the window list at the top of the sidebar:

```yaml
sidebar:
  header:
    height: 0
    padding_bottom: 0
```

`line_height` inserts blank lines between rows. `0` is compact; `1` or more
spaces things out, which helps on a touch screen.

The three buttons are off by default. Turn them on if you prefer clicking to
`prefix + c` and `prefix + G`:

```yaml
sidebar:
  new_tab_button: true
  new_group_button: true
  close_button: true
```

## Sort order

```yaml
sidebar:
  sort_by: "group"   # or "index"
```

`group` clusters windows under their group headers. `index` lists them in plain
tmux window order.

## Tree glyphs and disclosure icons

```yaml
sidebar:
  colors:
    disclosure_expanded: "⊟"
    disclosure_collapsed: "⊞"
    active_indicator_frames: ["▶", "▶", "▶", "▶", "▶", " "]
    tree_branch: "├─"
    tree_branch_last: "└─"
    tree_connector: "─"
    tree_connector_panes: "┬"
    tree_continue: "│"
```

`active_indicator_frames` animates the marker on the active row. Six identical
frames followed by a space gives a slow blink; a single-element list makes it
static.

ASCII equivalents for fonts that render box drawing badly are in
[Themes](Themes.md#adjusting-a-theme).

## Pane headers

```yaml
sidebar:
  pane_headers: true
```

This enables the clickable overlay headers on content panes, replacing tmux's
native border titles. Details in [Pane Headers](Pane-Headers.md).

## Remote tabs

When a window is inside an ssh or mosh session, Tabby can mark it:

```yaml
sidebar:
  ssh_icon: "󰒋"
```

With an icon set, the tab renders on one line prefixed by that glyph. Leave it
empty for the older two-line layout, host name stacked above the tab name.
Per-host colours and grouping are in
[SSH and Remote Hosts](SSH-and-Remote-Hosts.md).

## Debug logging

```yaml
sidebar:
  debug: true
```

Writes to `/tmp/tabby-debug.log`. Leave it off in normal use; see
[Troubleshooting](Troubleshooting.md).
