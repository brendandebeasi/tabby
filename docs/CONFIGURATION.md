# Configuration

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

# Prompt styling (for rename dialogs, etc.)
prompt:
  fg: "#000000"      # Text color
  bg: "#f0f0f0"      # Background color
  bold: true         # Bold text
```

## Notes

- `position`: `top`, `bottom`, `left`, or `right`.
- `height`: lines (horizontal) or columns (vertical).
- `groups`: first match wins; add `Default` as a fallback.
- `prompt`: styling for rename and command prompts (black on light for legibility).
