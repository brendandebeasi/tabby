[Home](Home.md) › Themes

# Themes

A theme sets the sidebar's own chrome: background, text, headers, tree lines and
selection colours. Window group colours are separate and are covered in
[Groups and Colors](Groups-and-Colors.md).

## Choosing one

```yaml
sidebar:
  theme: rose-pine-dawn
```

## Built-in presets

Defined in `pkg/colors/themes.go`.

| Light | Dark |
|---|---|
| `rose-pine-dawn` | `rose-pine` |
| `catppuccin-latte` | `rose-pine-moon` |
| `solarized-light` | `catppuccin-mocha` |
| `gruvbox-light` | `dracula` |
| | `nord` |
| | `solarized-dark` |
| | `gruvbox-dark` |
| | `tokyo-night` |
| | `one-dark` |
| | `dark` |

`default` is the palette used when nothing is configured.

Pick one that matches your terminal background. A light theme on a dark terminal
leaves the sidebar glowing next to your content, which is the single most common
reason people think the colours look broken.

## Light and dark toggle

Tabby pairs a light theme with a dark one and switches between them on demand.
Nothing inspects the OS appearance setting; the choice is yours.

```yaml
auto_theme:
  enabled: true
  mode: dark              # which of the two is selected right now
  light: rose-pine-dawn
  dark: rose-pine
```

Switch with `prefix + T`, or from any shell in the session:

```sh
tabby theme          # print the current selection
tabby theme toggle   # flip
tabby theme light    # select a specific variant
tabby theme dark
```

Toggling writes `mode` back to `config.yaml`, so the choice survives a daemon
restart. It repaints the sidebar, the pane borders and the global tmux
`window-style` immediately.

This works over SSH with nothing forwarded. The daemon on the remote host owns
the setting, so toggling in a remote session changes that host's sidebar and
leaves your laptop alone.

`tabby setup` will offer to pick the pair for you.

## Adjusting a theme

Individual pieces can be overridden without forking a preset:

```yaml
sidebar:
  theme: rose-pine
  header:
    fg: "#e0def4"
    bg: "#1f1d2e"
    active_color: "#eb6f92"
  colors:
    inactive_lighten: 0.15
    disclosure_expanded: "⊟"
    disclosure_collapsed: "⊞"
    tree_branch: "├─"
    tree_branch_last: "└─"
    tree_connector: "─"
    tree_connector_panes: "┬"
    tree_continue: "│"
```

`inactive_lighten` controls how far inactive rows drift from the theme
background. Raise it if inactive windows are hard to read, lower it if they are
too loud.

Swap the tree glyphs for ASCII if your font renders box drawing badly:

```yaml
sidebar:
  colors:
    tree_branch: "|-"
    tree_branch_last: "`-"
    tree_connector: "-"
    tree_continue: "|"
    disclosure_expanded: "-"
    disclosure_collapsed: "+"
```

## Terminal background

Set `pane_header.terminal_bg` to your actual terminal background colour.
Tabby cannot query it, and several blending decisions depend on it:

```yaml
pane_header:
  terminal_bg: "#191724"
```

A mismatch shows up as a band of the wrong colour along pane header edges.
