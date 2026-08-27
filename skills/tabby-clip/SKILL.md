---
name: "Tabby Clip"
description: "Push text from this machine up to the clipboard of the device the user is physically sitting at (laptop or phone), over OSC 52 through tmux and mosh/ssh. Use when the user asks to copy something to their clipboard, send output to their phone, or get a snippet off a remote box — especially on a host reached through ssh where the local pbcopy/xclip would write to the wrong machine's clipboard."
---

# Tabby Clip

## What this does

Puts text on the clipboard of the device the user is actually looking at.

That is usually not the machine you are running on. A typical session is
`phone or laptop → mosh → the user's Mac → ssh → this host`. Running `pbcopy`
or `xclip` here writes to a clipboard nobody can paste from. This skill sends
the text back up the chain instead.

## When to use it

- The user asks you to copy something, or says "put that on my clipboard".
- The user wants output on their phone — a stack trace, a URL, a generated
  token, a diff — to paste somewhere else.
- You produced something short the user will want in another app.

Do not use it for large output. It carries text only, capped at 64 KiB, and it
overwrites whatever the user had copied. Write bigger things to a file and send
the path.

## How to call it

Three interchangeable forms. Try the binary first, then the helper script, then
the shell function.

```bash
# Where the tabby binary exists (the user's Mac):
printf '%s' "$text" | tabby clip send
tabby clip send --text 'a literal string'
tabby clip send --file ./report.txt
tabby clip send --pane            # this pane's last 100 lines

# On a remote host with no binary (the client-* boxes):
printf '%s' "$text" | ~/bin/tabby-clip
~/bin/tabby-clip 'a literal string'

# Same script sourced from a shell rc, where it is also a function:
make test 2>&1 | tabby-clip-tail 200
```

`~/bin` is not on PATH on the client boxes, so call the script by its full
path. Pick between the three by probing, so one instruction works everywhere:

```bash
if command -v tabby >/dev/null 2>&1; then
  printf '%s' "$text" | tabby clip send --quiet
elif [ -x "$HOME/bin/tabby-clip" ]; then
  printf '%s' "$text" | "$HOME/bin/tabby-clip"
else
  printf '%s' "$text" | tabby-clip
fi
```

## Confirming it worked

You cannot read the clipboard back — the transport is write-only, by design.
A zero exit status means the escape was written to the terminal, not that the
clipboard changed. Tell the user what you sent and let them check.

If nothing arrives, the cause is almost always a setting somewhere else on the
chain rather than anything on this host. Point the user at
`docs/wiki/SSH-and-Remote-Hosts.md`. The checks are `set -g set-clipboard on`
on every tmux in the chain, `set -g allow-passthrough on` on any tmux running
*here* on the remote host, and a terminal app that honours OSC 52.

## Direction

This is one-way, upward. Reading the user's clipboard from here is not
possible: mosh implements only the write half of OSC 52, so there is no query
to answer. If you need something from the user's clipboard, ask them to paste
it.
