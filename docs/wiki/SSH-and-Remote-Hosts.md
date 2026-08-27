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

### Sending without selecting

Dragging a selection is fine on a laptop and impossible on a phone. `prefix + Y`
sends the focused pane's last 100 lines to your clipboard instead, and the same
thing is available as a command:

```bash
tabby clip send --text 'a literal string'
tabby clip send --file ./report.txt
tabby clip send --pane --lines 300
some-command | tabby clip send
```

It carries text only, capped at 64 KiB, keeping the tail when it has to cut.

### From a host with no Tabby

The client-* boxes do not need the binary. Copy `scripts/tabby-clip.sh` to the
remote host and source it from the shell rc:

```bash
# on your Mac
scp scripts/tabby-clip.sh client-b1:~/.tabby-clip.sh

# in the remote ~/.bashrc or ~/.zshrc
[ -f ~/.tabby-clip.sh ] && . ~/.tabby-clip.sh
```

That defines `tabby-clip` and `tabby-clip-tail`, which emit the same escape in
pure shell:

```bash
tabby-clip 'a literal string'
cat report.txt | tabby-clip
make test 2>&1 | tabby-clip-tail 200
```

The same file is also executable, which is the form to reach for on a host
where you do not control the shell rc — an agent's box, or anything where a
non-interactive shell has to be able to send:

```bash
chmod +x ~/bin/tabby-clip
some-command | ~/bin/tabby-clip
```

If the remote shell is itself inside tmux, the escape is wrapped in a DCS
passthrough envelope so the remote tmux forwards it upstream rather than
consuming it. This happens automatically, on `$TMUX`, but the remote tmux has
to be willing:

```bash
set -g allow-passthrough on
```

Without it the envelope is swallowed whole. Nothing reaches the clipboard and
nothing reports an error — the send simply appears to do nothing, which is the
one failure here you cannot diagnose from the symptom.

### For agents on those boxes

An agent working on a remote host has the same problem in a worse form: it has
no way to know that `pbcopy` there is the wrong clipboard. Copy the skill and
the command over so it does:

```bash
# on this machine, once
mkdir -p ~/.claude/skills ~/.claude/commands
cp -r skills/tabby-clip ~/.claude/skills/
cp commands/clip.md ~/.claude/commands/

# and on each remote box
scp -r skills/tabby-clip client-b1:~/.claude/skills/
scp commands/clip.md client-b1:~/.claude/commands/
```

Both are host-agnostic — they probe for the `tabby` binary and fall back to the
shell function — so the same two files work on your Mac and on every client-*
box. `/clip` sends by hand; the skill is what gets picked up when you just ask
for something on your clipboard.

### Getting it working over mosh, on iOS

Mosh implements the write half of OSC 52 and accepts only the `c` selector.
Everything Tabby emits is already in that shape, and so is what tmux re-emits:
on 3.4 and 3.6, with stock `xterm-256color` or `tmux-256color`, tmux forwards
exactly `ESC ] 52 ; c ; <base64> BEL` to its client. `set-clipboard on` is the
only setting that matters.

You will find advice to add a `terminal-overrides` line supplying the `Ms`
terminfo capability, on the grounds that `xterm-256color` has none and tmux
sends nothing without it:

```bash
set -ag terminal-overrides ",xterm-256color:Ms=\\E]52;c%p1%.0s;%p2%s\\7"
```

That was true of much older tmux. It is not true of 3.4 or 3.6, which carry
their own built-in defaults — capturing the pty on both versions shows the same
bytes with the override and without it. The line is harmless, so keep it if you
already have it, but it is not the thing to reach for when a send goes missing.

The last piece is on the terminal app: it has to parse OSC 52 and write the
system clipboard. Writing the pasteboard is unrestricted on iOS — it is
*reading* that prompts — so this is the direction that works there.

If there is a second tmux on the remote host, it needs `set-clipboard on` too.
The escape chains as far as every layer forwards it.

### Only upward

This is one-way. OSC 52 defines a query form for reading the clipboard back,
but mosh implements only the write half, so there is nothing to answer a query
from your phone. Pulling a clip *down* needs a different channel entirely —
a helper process on the outer device, not a terminal escape.

Text is also the whole of it. OSC 52 carries base64 text and nothing else, so
there is no way to push an image up this way.
