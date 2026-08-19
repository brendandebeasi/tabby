[Home](Home.md) › Groups and Colors

# Groups and Colors

A group is a labelled, colour-coded bucket of windows. Windows land in one by
name pattern, by manual assignment, or by inheriting from the window they were
created from.

```
+---------------------------+
|  SIDEBAR                  |      SESSION
|                           |         |
|  Frontend  [group]        |         +-- Frontend
|    0. dashboard           |         |     +-- 0. dashboard
|    1. components          |         |     |     +-- pane 0: vim
|                           |         |     |     +-- pane 1: shell
|  Backend   [group]        |         |     +-- 1. components
|    2. api                 |         |
|    3. tests               |         +-- Backend
|                           |         |     +-- 2. api
|  Default   [group]        |         |     +-- 3. tests
|  > 4. vim                 |         |
|    5. notes               |         +-- Default
|                           |               +-- 4. vim  <- active
|  [+] New Tab              |               +-- 5. notes
+---------------------------+
```

## Defining groups

```yaml
groups:
  - name: "Frontend"
    pattern: "^FE\\|"
    working_dir: "~/projects/frontend"
    theme:
      bg: "#e74c3c"

  - name: "Backend"
    pattern: "^BE\\|"
    working_dir: "~/projects/backend"
    theme:
      bg: "#27ae60"

  - name: "Default"
    pattern: ".*"
    theme:
      bg: "#3498db"
```

`pattern` is a regex matched against the window name, and the first match wins.
Order matters: put a `.*` catch-all last or it swallows everything. Escape the
pipe as `\\|` inside a double-quoted YAML string.

`working_dir` is where new windows in that group start.

## Assigning a window to a group

By name, if it matches a pattern:

```bash
tmux rename-window 'FE|dashboard'
```

By right-clicking the window in the sidebar and picking Move to Group. The
assignment is stored on the window and outlives a later rename.

Programmatically:

```bash
tmux set-window-option -t :0 @tabby_group "Frontend"
```

Interactively, over the whole config:

```bash
tabby manage-group
```

Create a group on the fly with `prefix + G`, or by right-clicking a group header
and picking New window in group.

## Colours from one value

Give a group a `bg` and Tabby derives the rest:

```yaml
groups:
  - name: "StudioDome"
    theme:
      bg: "#8b1a1a"
```

Derived from that base:

| Derived | How it is chosen |
|---|---|
| `fg` | Black or white, whichever contrasts better |
| `active_bg` | A more saturated version of the base |
| `active_fg` | Text colour for the active row |
| `inactive_bg` | A desaturated version of the base |
| `inactive_fg` | Text colour for inactive rows |

Derived colours hold a 4.5:1 contrast ratio, the WCAG AA threshold, and are
checked against both light and dark terminal backgrounds.

Override any of them and the rest stay derived:

```yaml
groups:
  - name: "Project"
    theme:
      bg: "#3498db"
      active_fg: "#ffff00"
```

## No colours at all

With no `theme` block, groups are assigned from a 12-colour palette in order:
blue `#3498db`, green `#2ecc71`, red `#e74c3c`, purple `#9b59b6`, orange
`#f39c12`, then seven more.

## Icons

```yaml
groups:
  - name: "Frontend"
    theme:
      bg: "#e74c3c"
      icon: ""
      active_indicator_bg: "#c0392b"
```

Nerd Font glyphs, emoji and plain ASCII all work. With
[prompt integration](Configuration.md#prompt-integration) on, the group icon
also appears in your shell prompt.

## Per-window colour overrides

Right click a window and pick Set Tab Color. Options are red, orange, yellow,
green, blue, purple, pink, cyan, grey, transparent, or reset to the group
colour. Transparent drops the background entirely and leaves coloured text,
which suits a busy sidebar.

```bash
tmux set-window-option -t :0 @tabby_color "transparent"
tmux set-window-option -t :0 @tabby_color "#e91e63"
tmux set-window-option -t :0 -u @tabby_color   # back to the group colour
```

To hide the built-in swatches from the menu and offer only your own:

```yaml
sidebar:
  hide_predefined_colors: true
```

## Group colour changes

Right click a group header, pick Change group colour. It applies to every window
in the group that has no override of its own. Transparent works here too.

## Empty groups

By default a configured group shows even with no windows in it, so you can drop
a window into it. To hide empties:

```yaml
sidebar:
  show_empty_groups: false
```

## Sort order

```yaml
sidebar:
  sort_by: "group"   # or "index"
```

`group` keeps windows clustered under their group headers. `index` lists them in
tmux window order and ignores grouping, which is closer to a plain status bar.

## Pane borders that follow the group

```yaml
pane_header:
  border_from_tab: true
  auto_border: true
```

`border_from_tab` colours the active pane's border with the window's tab colour.
`auto_border` also sets tmux's global `pane-border-style` from it, so the
colours stay consistent when the sidebar is collapsed.
