[Home](Home.md) › Quick Start

# Quick Start

Assumes Tabby is [installed](Installation.md) and the sidebar is visible.

## 1. Click around

The sidebar is fully clickable, which is the main thing tmux's native status bar
cannot do.

- Left click a window to switch to it.
- Right click a window, pane, or group header for a context menu.
- Middle click a window to close it, with a confirmation.
- Drag the right edge to resize. Every other window at the same
  [profile](Responsive-Layout.md) follows.

## 2. Group your windows

Groups are matched by regex against the window name, first match wins. Open
`~/.config/tabby/config.yaml` and add:

```yaml
groups:
  - name: "Frontend"
    pattern: "^FE\\|"
    working_dir: "~/projects/frontend"
    theme:
      bg: "#e74c3c"

  - name: "Default"
    pattern: ".*"
    theme:
      bg: "#3498db"
```

Name a window `FE|dashboard` and it lands under Frontend. New windows created in
that group start in `~/projects/frontend`.

Only `bg` is needed. Foreground, active and inactive variants are derived from
it at WCAG AA contrast; see [Groups and Colors](Groups-and-Colors.md).

To move a window without renaming it, right click it and pick Move to Group.

## 3. Pick a theme

```yaml
sidebar:
  theme: rose-pine-dawn
```

Fourteen presets ship with Tabby. Light terminals suit `rose-pine-dawn`,
`catppuccin-latte`, `solarized-light` and `gruvbox-light`. Dark terminals suit
`rose-pine`, `catppuccin-mocha`, `tokyo-night` and `dracula`. Pair two and
switch with `prefix + T`; see [Themes](Themes.md).

## 4. Reclaim the space when you need it

`Cmd+Shift+\` stashes the sidebar and restores it instantly, because the
renderer keeps running in a hidden holding window. On a phone, tap the `≡`
button in the window header. From a shell:

```bash
tabby hook toggle-collapse-sidebar
```

The shortcut needs a terminal keybinding; see
[Keyboard and Mouse](Keyboard-and-Mouse.md#sending-cmdshift-to-tmux).

`prefix + Tab` is a different operation. It disables Tabby for the session
entirely, stopping the daemon.

## 5. Get told when a long job finishes

Install a notifier and point it at `tabby hook focus-pane`:

```bash
brew install growlrrr
```

```bash
TARGET=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}')
growlrrr send --title "Build done" --execute \
  "$HOME/.tmux/plugins/tabby/bin/tabby hook focus-pane $TARGET" "ready"
```

Clicking the notification raises the terminal and selects that exact pane. Set
`terminal_app` in `config.yaml` so Tabby knows which application to raise. Full
setup, including Claude Code and OpenCode hooks, is in
[Notifications and Deep Links](Notifications-and-Deep-Links.md).

## Where to go next

- [Widgets](Widgets.md) adds a clock, system stats, git status or a pet to the
  sidebar.
- [Pane Headers](Pane-Headers.md) puts a title bar on each pane and dims the
  inactive ones.
- [Responsive Layout](Responsive-Layout.md) explains how the same session looks
  right on a 27-inch monitor and an iPhone.
