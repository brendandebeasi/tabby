[Home](Home.md) › Responsive Layout

# Responsive Layout

Tabby sizes its chrome from the attached client's dimensions, so the same tmux
session is usable on a 27-inch monitor and on a phone attached to the same
server, with no reconfiguration when you switch.

## Profiles

Width is measured per window, in columns.

| Profile | Window width | Default sidebar width |
|---|---|---|
| Mobile | 110 or fewer | 15, then capped further |
| Tablet | 111 to 170 | 20 |
| Desktop | 171 or more | 25 |

Breakpoints and widths are tmux options:

```bash
tmux set -g @tabby_sidebar_mobile_max_window_cols 110
tmux set -g @tabby_sidebar_tablet_max_window_cols 170

tmux set -g @tabby_sidebar_width_mobile  15
tmux set -g @tabby_sidebar_width_tablet  20
tmux set -g @tabby_sidebar_width_desktop 25
```

The equivalents in `config.yaml`:

```yaml
sidebar:
  mobile_max_window_cols: 110
  tablet_max_window_cols: 140
  width_mobile: 15
  width_tablet: 20
  width_desktop: 25
```

The tablet breakpoint defaults to 170 in code, while the shipped `config.yaml`
sets 140. Either is fine; the point is that a value you set wins, and 140 makes
the tablet band narrower.

The mobile breakpoint is only accepted at 60 or above, and the tablet breakpoint
only at or above the mobile one, so a typo cannot invert the bands. Sidebar
widths below 15 are rejected for tablet and desktop.

## Extra caps on mobile

On a narrow client the configured mobile width is only a ceiling. Three limits
apply and the smallest wins:

```yaml
sidebar:
  width_mobile: 15
  mobile_max_percent: 15        # never more than 15% of the window
  mobile_min_content_cols: 40   # always leave 40 columns for content
```

On an 80-column phone client that gives 15% of 80 = 12, and 80 − 40 = 40, and
the configured 15. The smallest is 12, so the sidebar is 12 columns. The floor
is 10, so the sidebar never disappears entirely.

Raise `mobile_min_content_cols` if your content is getting squeezed, and lower
`mobile_max_percent` if the sidebar is taking too large a share.

## Window header on phones

Narrow clients get a one-row header pane above each window with five buttons:

| Button | Action |
|---|---|
| `◀` | Previous window |
| `≡` | Collapse or restore the sidebar |
| `+` | New window |
| `×` | Close window |
| `▶` | Next window |

This is how you drive Tabby on a touch screen without a keyboard.

## Keyboard clamp

When an on-screen keyboard appears, the client's height drops sharply. Below the
threshold Tabby clamps the sidebar to a narrower width for about four seconds so
the keyboard does not crush the content pane.

```bash
tmux set -g @tabby_sidebar_mobile_keyboard_rows 38
tmux set -g @tabby_sidebar_width_mobile_keyboard 10
```

Lower the row count if the clamp fires when the keyboard is not up, raise it if
it fails to fire when the keyboard is up.

## Width sync across windows

Drag the sidebar border in one window and every other window at the same profile
follows on the next tick. The new value is written to
`@tabby_sidebar_width_<profile>` on the active window, so it survives a daemon
restart.

Windows at a different profile are left alone. Resizing on a laptop does not
change what your phone sees.

## Mobile terminal setup

Blink Shell on iPad and iPhone is the combination this was built against.
Configure it to send the collapse shortcut:

```json
{
  "keys": "cmd+shift+\\",
  "action": "hex",
  "value": "1b5b39323b3675"
}
```

More terminals in
[Keyboard and Mouse](Keyboard-and-Mouse.md#sending-cmdshift-to-tmux).

Mouse events do not survive mosh, so use ssh from a mobile client if you want to
tap the sidebar. See
[SSH and Remote Hosts](SSH-and-Remote-Hosts.md#mosh-and-mouse-events).
