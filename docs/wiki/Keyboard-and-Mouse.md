[Home](Home.md) › Keyboard and Mouse

# Keyboard and Mouse

Tabby leaves standard tmux keys alone where it can, and rebinds a handful so
that the sidebar stays in sync with what you did. `prefix` is whatever you have
configured, `Ctrl+b` unless you changed it.

## Window and pane keys

| Key | Action |
|---|---|
| `prefix + c` | New window, inheriting the current group's working directory and colour |
| `prefix + n` / `prefix + p` | Next / previous window |
| `prefix + 1`…`9` | Switch to window by index |
| `prefix + w` | Window picker (`choose-tree -Zw`) |
| `prefix + s` | Session picker (`choose-tree -Zs`) |
| `prefix + ,` | Pane menu for the current pane |
| `prefix + r` | Rename the window and lock the name |
| `prefix + "` or `prefix + -` | Split vertically |
| `prefix + %` or `prefix + \|` | Split horizontally |
| `prefix + x` | Close pane, with confirmation |
| `prefix + &` or `prefix + k` | Close window, with confirmation |
| `prefix + o` | Cycle to the next content pane, skipping sidebar and headers |
| `prefix + d` | Detach |

Renaming through `prefix + r` sets `@tabby_name_locked 1`, so automatic naming
stops overwriting your choice. Right click the window in the sidebar and pick
Unlock Name to hand it back.

## Tabby keys

| Key | Action |
|---|---|
| `prefix + Tab` | Enable or disable Tabby for this session |
| `prefix + T` | Toggle between the configured light and dark themes |
| `prefix + G` | Create a new group, prompting for a name |
| `prefix + m` | Minimize or restore the current window |
| `prefix + 0` or `prefix + g` | Open the [dashboard](Dashboard.md) |
| `prefix + L` | Dashboard layout picker |
| `prefix + P` | Promote the focused pane to the main slot |
| `prefix + Y` | Send this pane's last 100 lines to the clipboard of the device you are at |
| `prefix + {` / `prefix + }` | Move the focused pane back / forward one slot |
| `Cmd+Shift+\` | Collapse or restore the sidebar |
| `Alt+;` / `Alt+'` | Previous / next window without the prefix |
| `Alt+\`` | Cycle the active content pane |

`prefix + Tab` and `Cmd+Shift+\` are easy to confuse. The first stops the daemon
and unhooks Tabby from the session. The second stashes the sidebar pane into a
hidden holding window and brings it back instantly, leaving everything running.

When the sidebar pane itself has focus, `m` opens the marker picker for the
active window.

## Mouse

Mouse support is the reason the sidebar exists as a pane rather than a status
line. Enable it in tmux if you have not:

```bash
set -g mouse on
```

### In the sidebar

| Action | Result |
|---|---|
| Left click a window or pane | Switch to it |
| Left click the disclosure icon | Collapse or expand that group |
| Left click the right edge | Collapse the sidebar |
| Middle click a window | Close it, with confirmation |
| Drag the right border | Resize; other windows at the same profile follow |
| Scroll | Scroll the window list |

Clicking a group header row does not toggle it. Only the `⊞` / `⊟` disclosure
icon does, so that the rest of the row can carry the right-click menu without
misfiring.

### Right-click menus

On a window:

- Rename, and Unlock Name to undo the lock
- Collapse or Expand Panes
- Move to Group
- Set Marker, with a searchable emoji picker
- Set Tab Color, including a transparent option
- Split Right or Split Down
- Open in Finder
- Kill window

On a pane:

- Rename pane, and Unlock pane name
- Split pane
- Focus pane
- Promote to Primary
- Break to new window
- Close pane

On a group header:

- New window in group
- Collapse or Expand group
- Rename group
- Change group colour
- Set Marker
- Set working directory
- Delete group
- Close all windows in group

### Elsewhere

Right click a pane border for the Pane Actions menu. Drag with the left button
to select text; the selection is copied through OSC 52, which works over SSH.

## Sending Cmd+Shift+\ to tmux

Tabby binds `Ctrl+Shift+\` in tmux, reached through CSI u encoding. Your
terminal has to send the bytes `\x1b[92;6u`. That needs extended keys enabled:

```bash
set -g extended-keys on
set -sa terminal-features 'xterm*:extkeys'
```

**Ghostty**, in `~/.config/ghostty/config`:

```
keybind = super+shift+backslash=text:\x1b[92;6u
keybind = cmd+left_bracket=text:\x1b{
keybind = cmd+right_bracket=text:\x1b}
keybind = cmd+grave_accent=text:\x1b`
```

The bracket bindings give you `Cmd+[` and `Cmd+]` for previous and next window,
leaving `Cmd+Shift+[`/`]` for Ghostty's own tabs. The backtick binding drives
pane cycling and overrides macOS's cycle-windows shortcut inside Ghostty.

macOS binds `Cmd+Shift+\` to Show All Tabs. Free it up, then restart Ghostty:

```bash
defaults write com.mitchellh.ghostty NSUserKeyEquivalents -dict-add "Show All Tabs" '\0'
```

**Blink Shell** on iPad, in `kb.json`:

```json
{
  "keys": "cmd+shift+\\",
  "action": "hex",
  "value": "1b5b39323b3675"
}
```

`1b5b39323b3675` is `\x1b[92;6u` in hex.

**iTerm2**: Preferences → Profiles → Keys, shortcut `⌘⇧\`, action Send Escape
Sequence, value `[92;6u`.

**kitty**, in `kitty.conf`:

```
map cmd+shift+backslash send_text all \x1b[92;6u
```

Any terminal that can send an arbitrary escape sequence will work. Map your key
of choice to `ESC [ 9 2 ; 6 u`.

## Changing bindings

Keys come from the `bindings` block in `config.yaml`:

```yaml
bindings:
  toggle_sidebar: "prefix + Tab"
  next_tab: "prefix + n"
  prev_tab: "prefix + p"
  next_window_global: "cmd+]"
  prev_window_global: "cmd+["
  new_window_global: "cmd+shift+n"
  kill_window_global: "cmd+shift+w"
  toggle_minimize_window: "cmd+shift+m"
  swap_pane: "cmd+`"
```

Entries beginning with `prefix +` become prefix-table bindings; the rest become
root-table bindings with `-n`. Reload with `tmux source ~/.tmux.conf` after
editing.
