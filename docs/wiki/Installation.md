[Home](Home.md) › Installation

# Installation

## Requirements

- tmux 3.2 or newer. Extended keys, needed for the `Cmd+Shift+\` collapse
  binding, landed in 3.2.
- Go 1.21 or newer, to build the binaries.
- A terminal that reports mouse events. Ghostty, iTerm2, kitty, Alacritty,
  WezTerm and Terminal.app all work. Mosh does not; see
  [Troubleshooting](Troubleshooting.md#mouse-clicks-do-nothing).
- A Nerd Font if you want the default icons. Tabby also ships emoji and ASCII
  glyph sets, so a plain font is fine.

## Install with TPM

Add the plugin to `~/.tmux.conf`:

```bash
set -g @plugin 'brendandebeasi/tabby'
```

Reload tmux and install:

```bash
tmux source ~/.tmux.conf
```

Then press `prefix + I`.

## Install manually

```bash
git clone https://github.com/brendandebeasi/tabby ~/.tmux/plugins/tabby
cd ~/.tmux/plugins/tabby
./scripts/install.sh
```

Add the plugin line to `~/.tmux.conf`:

```bash
run-shell ~/.tmux/plugins/tabby/tabby.tmux
```

`scripts/install.sh` compiles everything under `cmd/` into `bin/` and creates a
starter `~/.config/tabby/config.yaml` if you do not already have one.

## First run

Reload tmux. The sidebar starts on its own. If nothing appears:

```bash
tmux show -gv @tabby_enabled     # must not be 0
ls ~/.tmux/plugins/tabby/bin/    # tabby, render-status, render-tab, ...
```

To configure interactively rather than editing YAML by hand:

```bash
tabby setup
```

The wizard writes group patterns, a light/dark theme pair, and the terminal app
used for [deep links](Notifications-and-Deep-Links.md).

## Recommended tmux settings

Extended keys are required for the collapse shortcut and improve key handling
generally:

```bash
set -g extended-keys on
set -sa terminal-features 'xterm*:extkeys'
```

If you want sessions to survive a reboot, install
[tmux-resurrect](Session-Persistence.md) before the Tabby plugin line.

## Turning Tabby off

`prefix + Tab` stops the daemon for the current session and removes the hooks.
To keep the plugin loaded but inert everywhere:

```bash
tmux set -g @tabby_enabled 0
```

## Uninstall

```bash
tmux set -g @tabby_enabled 0
# remove the @plugin or run-shell line from ~/.tmux.conf
rm -rf ~/.tmux/plugins/tabby ~/.config/tabby ~/.local/state/tabby
tmux kill-server
```

Next: [Quick Start](Quick-Start.md).
