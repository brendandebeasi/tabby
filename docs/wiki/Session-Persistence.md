[Home](Home.md) › Session Persistence

# Session Persistence

[tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect) saves and
restores tmux sessions across server restarts and reboots. Tabby detects it and
wires itself in automatically.

## Why it needs special handling

The sidebar and pane headers are real tmux panes. Saved naively, they come back
as dead shell panes with nothing running in them, and you get a broken layout
with Tabby's own panes duplicated on top.

So Tabby strips its utility panes on save and rebuilds them on restore.

| Preserved by resurrect | Rebuilt by Tabby |
|---|---|
| Window layout and names | Sidebar |
| Pane working directories | Pane headers |
| Running programs | Daemon process |
| Global options, including `@tabby_sidebar` | Mouse state cleanup |
| Groups and custom colours | Runtime files |

## Setup

With TPM, add resurrect **before** the Tabby plugin line in `~/.tmux.conf`:

```bash
set -g @plugin 'tmux-plugins/tmux-resurrect'
```

Press `prefix + I`, or run `tmux source ~/.tmux.conf`. That is the whole setup.

Without TPM:

```bash
git clone https://github.com/tmux-plugins/tmux-resurrect ~/.tmux/plugins/tmux-resurrect
```

```bash
run-shell ~/.tmux/plugins/tmux-resurrect/resurrect.tmux
run-shell ~/.tmux/plugins/tabby/tabby.tmux
```

Order matters in both cases. Resurrect has to be loaded when Tabby looks for it.

## Using it

- `prefix + Ctrl-s` saves. Tabby strips its panes from the save file first.
- `prefix + Ctrl-r` restores. Tabby clears stale runtime state, kills leftover
  processes, and starts the sidebar in the mode you saved.

## Existing resurrect hooks

Tabby only sets the resurrect hook options when they are unset or already owned
by Tabby, so your own hooks are never clobbered. To run both, chain them:

```bash
#!/usr/bin/env bash
# my-resurrect-restore-wrapper.sh
/path/to/your/custom-hook.sh
~/.tmux/plugins/tabby/bin/tabby hook resurrect-restore
```

Point the resurrect option at the wrapper.

## After a restore

If the sidebar does not come back, toggle it: `prefix + Tab` twice. Stale runtime
files from the previous server are the usual cause, and the toggle clears them.
See [Troubleshooting](Troubleshooting.md).
