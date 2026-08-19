[Home](Home.md) › SSH and Remote Hosts

# SSH and Remote Hosts

Tabby detects when a pane is inside an ssh or mosh session by reading the
command line locally. Nothing has to be installed on the remote host.

## Marking remote tabs

```yaml
sidebar:
  ssh_icon: "󰒋"
```

With an icon set, a remote tab renders on one line prefixed by the glyph. The
value above is Nerd Font `nf-md-ssh`. Leave it empty for the older layout, which
puts the host name on its own row above the tab name.

## Per-host colours and grouping

```yaml
sidebar:
  remote_hosts:
    - match: "client-gunpowder-*"
      color: "#ff8800"
      icon: "🔥"
      group: "Gunpowder"
    - match: "*.prod"
      color: "#cc2222"
```

`match` is a case-insensitive glob against the destination host. The first
matching rule wins, so list specific patterns before broad ones. `color`, `icon`
and `group` are each optional; omit one to leave it alone.

`group` matters because the directory-to-group rules cannot see the far end of
an ssh connection. Without it, every remote tab lands in your catch-all group no
matter which host it is on.

A colour you set by hand, or a remembered per-host appearance, always beats
these defaults, so marking one production window red by hand is not undone by a
rule.

Giving production hosts an unmistakable colour is worth the two lines of config.

## Inheriting the connection

When you split a pane that is ssh'd somewhere, Tabby re-runs the same connection
in the new pane so it lands on the same host:

```yaml
sidebar:
  split_inherit_ssh: true     # default
  new_tab_inherit_ssh: false  # default
```

`split_inherit_ssh` covers `prefix + |`, `prefix + -` and the header split
buttons. `new_tab_inherit_ssh` does the same for new tabs opened from a remote
tab; it is off by default because a new tab is more often the start of separate
local work.

## Bell notifications from remote commands

To get a notification when a remote command finishes, make the remote shell ring
the bell each time its prompt is drawn.

If you control the remote host, add to its `~/.bashrc`:

```bash
export PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }printf '\a'"
```

This is the option that will not surprise you later.

To cover every host from the client side instead, add to `~/.ssh/config`:

```ssh
Host *
  RemoteCommand bash -c 'PROMPT_COMMAND="printf \"\a\""; exec bash -l'
  RequestTTY force
```

`RemoteCommand` applies to every ssh invocation, including the ones made by
`scp`, `rsync` and `git`, and it breaks them. Exempt the hosts that need it:

```ssh
Host github.com gitlab.com bitbucket.org
  RemoteCommand none
  RequestTTY auto
```

The bell shows up as the `bell` indicator on the window; see
[AI Tool Indicators](AI-Tool-Indicators.md#appearance) to restyle it.

## Themes over ssh

Run Tabby on the remote host and `prefix + T` toggles that host's theme. The
daemon there owns the setting and writes it to that host's `config.yaml`.
Nothing needs forwarding from your laptop, and your local session is unaffected.

## Mosh and mouse events

Mosh strips mouse escape sequences. Over mosh, sidebar clicks, right-click menus
and middle-click close all do nothing. Keyboard navigation is unaffected.

If you want the mouse, use ssh. If you want mosh's roaming and local echo, drive
Tabby from the keyboard, or from the phone window header buttons, which are
keystrokes underneath.

## Clipboard

Drag-select in the sidebar copies through OSC 52, which travels over ssh. Your
terminal has to allow it; most do by default, and tmux needs:

```bash
set -g set-clipboard on
```
