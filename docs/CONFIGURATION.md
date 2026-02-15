# Configuration

## Table of Contents

- [Example Config](#example-config)
- [Sidebar Position and Mode](#sidebar-position-and-mode)
- [Tips](#tips)

## Example Config

`config.yaml` fields:

```yaml
position: top
height: 2

style:
  rounded: true
  separator_left: ""
  separator_right: ""

overflow:
  mode: scroll
  indicator: "›"

groups:
  - name: "StudioDome"
    pattern: "^SD\\|"
    theme:
      bg: "#e74c3c"
      fg: "#ffffff"
      active_bg: "#c0392b"
      active_fg: "#ffffff"
      icon: ""

bindings:
  toggle_sidebar: "prefix + Tab"
  next_tab: "prefix + n"
  prev_tab: "prefix + p"

sidebar:
  new_tab_button: true
  close_button: false
  sort_by: "group"  # "group" or "index"

pane_header:
  active_fg: "#ffffff"
  active_bg: "#3498db"
  inactive_fg: "#cccccc"
  inactive_bg: "#333333"
  command_fg: "#aaaaaa"
  border_from_tab: true   # Use tab color for active pane border
  auto_border: false      # Auto-set tmux pane-border-style from window's group/custom color

# Prompt styling (for rename dialogs, etc.)
prompt:
  fg: "#000000"      # Text color
  bg: "#f0f0f0"      # Background color
  bold: true         # Bold text
```

## Sidebar Position and Mode

Control where the sidebar appears and how it attaches to the window using tmux options:

```bash
# Position: "left" (default) or "right"
tmux set-option -g @tabby_sidebar_position left

# Mode: "full" (default) or "partial"
tmux set-option -g @tabby_sidebar_mode full

# Width (existing option)
tmux set-option -g @tabby_sidebar_width 25
```

**Position** controls which side of the window the sidebar appears on:
- `left` -- sidebar on the left, main content on the right (default)
- `right` -- sidebar on the right, main content on the left

**Mode** controls how the sidebar pane is created:
- `full` -- sidebar spans the full window height, independent of pane splits (default)
- `partial` -- sidebar attaches to the current pane only, respecting existing vertical splits

These can also be set in `config.yaml` under the `sidebar` key:

```yaml
sidebar:
  position: left    # "left" or "right"
  mode: full        # "full" or "partial"
```

After changing position or mode, toggle the sidebar off and on to apply:
```bash
# prefix + Tab (twice) to toggle off then on
```

## Tips

- `position`: `top`, `bottom`, `left`, or `right`.
- `height`: lines (horizontal) or columns (vertical).
- `groups`: first match wins; add `Default` as a fallback.
- `prompt`: styling for rename and command prompts (black on light for legibility).
