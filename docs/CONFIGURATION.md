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
```

## Notes

- `position`: `top`, `bottom`, `left`, or `right`.
- `height`: lines (horizontal) or columns (vertical).
- `groups`: first match wins; add `Default` as a fallback.
