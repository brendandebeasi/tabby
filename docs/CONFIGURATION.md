# Configuration

## Table of Contents

- [Example Config](#example-config)
- [Sidebar Position and Mode](#sidebar-position-and-mode)
- [What a New Tab Opens On](#what-a-new-tab-opens-on)
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

## What a New Tab Opens On

A new tab opens on a shell. `landing` puts something in front of that shell
instead:

```yaml
landing:
  enabled: true
  command: eval "$(pounce --target current)"
```

`enabled` is off unless you set it, because a new tab that lands somewhere
unexpected is worse than one that does not. `command` is optional and names the
launcher; leave it out for tabby's own `tabby landing`.

Three things to know about the value:

- It is a **shell command line**, typed into the new tab's own interactive
  shell, so quote your own paths. It is sent byte for byte — tabby does not
  re-quote it, because that would break the `eval` it probably needs.
- The `eval "$(...)"` shape matters for a launcher that offers directories or
  hosts. A launcher writes its choice to stdout and draws its interface on
  stderr; `eval` runs that choice in the shell that stays behind, so a chosen
  `cd` sticks and a chosen `ssh` becomes the pane's foreground process — which
  is what tabby reads to give the tab its remote icon and host color. A launcher
  run as a child process can do neither.
- A value naming something that is missing or not executable costs one
  "command not found" and leaves you at the prompt. That is a usable shell, not
  a broken tab.

Two things override it. A tab spawned off an ssh tab inherits that connection
and goes straight to the host, launcher or no launcher — it is asking for that
host, not for a chance to pick another. And splits are exempt: this is about
tabs.

## Tips

- `position`: `top`, `bottom`, `left`, or `right`.
- `height`: lines (horizontal) or columns (vertical).
- `groups`: first match wins; add `Default` as a fallback.
- `prompt`: styling for rename and command prompts (black on light for legibility).
