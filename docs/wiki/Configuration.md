[Home](Home.md) › Configuration

# Configuration

## Where the config lives

`~/.config/tabby/config.yaml`. Set `TABBY_CONFIG_DIR` to point somewhere else,
which is how the test and demo harnesses keep out of your real setup:

```bash
TABBY_CONFIG_DIR=/tmp/tabby-experiment tmux new-session
```

`scripts/install.sh` writes a starter file if none exists. To build one
interactively instead:

```bash
tabby setup
```

Most changes are picked up by the daemon within a second. Binding changes need
`tmux source ~/.tmux.conf`, and sidebar position or mode changes need a collapse
and restore.

## The top-level shape

```yaml
position: top             # legacy horizontal bar: top, bottom, left, right
height: 2                 # rows if horizontal, columns if vertical

terminal_title:           # what the terminal emulator's title shows
  enabled: true
  format: "#{session_name}: #{window_name}"

style:                    # legacy horizontal bar separators
  rounded: true
  separator_left: ""
  separator_right: ""

overflow:
  mode: scroll
  indicator: "›"

groups: []                # see Groups and Colors
bindings: {}              # see Keyboard and Mouse
sidebar: {}               # see Sidebar
pane_header: {}           # see Pane Headers
prompt: {}                # shell prompt integration, below
indicators: {}            # see AI Tool Indicators
busy_detection: {}        # see AI Tool Indicators
widgets: {}               # see Widgets
auto_theme: {}            # see Themes

terminal_app: Ghostty     # for notification deep links
```

Every block is optional. Anything you leave out uses a default.

## Prompt integration

Tabby can push the active window's group colour and icon into your shell prompt,
so the prompt matches the tab you are looking at.

```yaml
prompt:
  bold: true
  shell_integration: true
  fallback_icon: "❯"
```

With `shell_integration` on, Tabby sets `@tabby_prompt_icon` and
`@tabby_pane_active` per window. Your shell reads them. `scripts/tabby-prompt.sh`
has a working zsh setup to copy.

## Terminal application

Deep links need to know which application to raise:

```yaml
terminal_app: Ghostty
```

Accepted values: `Ghostty`, `iTerm`, `Terminal`, `Alacritty`, `kitty`,
`WezTerm`. See [Notifications and Deep Links](Notifications-and-Deep-Links.md).

## Terminal background

Several colour decisions depend on knowing your actual terminal background.
Tabby cannot read it, so tell it:

```yaml
pane_header:
  terminal_bg: "#191724"
```

Getting this wrong shows up as a visible seam around pane headers where Tabby's
idea of the background does not match the real one.

## Config versus tmux options

Some settings exist in both places. tmux options win, because they can be
changed at runtime without a reload:

```bash
tmux set -g @tabby_sidebar_position right
tmux set -g @tabby_sidebar_width 30
```

The full list is in [tmux Options](tmux-Options.md).

## Validating a change

If the sidebar stops rendering after an edit, the YAML is usually at fault:

```bash
python3 -c 'import yaml,sys; yaml.safe_load(open(sys.argv[1]))' \
  ~/.config/tabby/config.yaml
```

Then check the daemon log:

```bash
tail -20 /tmp/tabby-daemon-$(tmux display -p '#{session_id}' | tr -d '$')-events.log
```

More in [Troubleshooting](Troubleshooting.md).
