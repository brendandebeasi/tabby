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

Two interchangeable forms. Try the binary first; fall back to the shell
function on hosts that do not have tabby installed.

```bash
# Where the tabby binary exists (the user's Mac):
printf '%s' "$text" | tabby clip send
tabby clip send --text 'a literal string'
tabby clip send --file ./report.txt
tabby clip send --pane            # this pane's last 100 lines

# On a remote host with no binary (the client-* boxes), where
# ~/.tabby-clip.sh is sourced from the shell rc:
printf '%s' "$text" | tabby-clip
tabby-clip 'a literal string'
make test 2>&1 | tabby-clip-tail 200
```

Pick between them by probing, so one instruction works on every host:

```bash
if command -v tabby >/dev/null 2>&1; then
  printf '%s' "$text" | tabby clip send --quiet
else
  printf '%s' "$text" | tabby-clip
fi
```

## Confirming it worked

You cannot read the clipboard back — the transport is write-only, by design.
A zero exit status means the escape was written to the terminal, not that the
clipboard changed. Tell the user what you sent and let them check.

If nothing arrives, the cause is almost always one of three settings on the
user's Mac rather than anything on this host. Point them at
`docs/wiki/SSH-and-Remote-Hosts.md`; the checks are `set -g set-clipboard on`,
an `Ms` entry in `terminal-overrides`, and a terminal app that honours OSC 52.

## Direction

This is one-way, upward. Reading the user's clipboard from here is not
possible: mosh implements only the write half of OSC 52, so there is no query
to answer. If you need something from the user's clipboard, ask them to paste
it.
