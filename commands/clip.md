---
description: Send text up to the clipboard of the device you are sitting at
argument-hint: "[what to send — a file path, a description, or nothing for the last output]"
---

Send something to the user's clipboard — the clipboard of the device they are
physically at, not this machine's.

What to send: $ARGUMENTS

If that is empty, send the most useful thing from the conversation so far: the
last command's output, the last file you produced, or the answer you just gave.
Ask only if there is genuinely no obvious candidate.

Use the tabby clip path, picking the form that exists on this host:

```bash
if command -v tabby >/dev/null 2>&1; then
  printf '%s' "$text" | tabby clip send --quiet
elif [ -x "$HOME/bin/tabby-clip" ]; then
  printf '%s' "$text" | "$HOME/bin/tabby-clip"
else
  printf '%s' "$text" | tabby-clip
fi
```

For a file, prefer `tabby clip send --file PATH` over reading it into a
variable. For a pane's recent output, `tabby clip send --pane --lines N`.

Keep it under 64 KiB — the transport truncates past that, keeping the tail.
For anything larger, send the path instead of the contents and say so.

Then tell the user in one line what landed on their clipboard. Do not claim to
have verified it: the channel is write-only and there is nothing to read back.
