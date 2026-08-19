# Architecture

Tabby is one Go binary that behaves differently depending on the subcommand
tmux invokes it with. A session runs one daemon, one sidebar renderer per
window, and one pane-header renderer per content pane.

## The processes

```
tmux session $0
  |
  +-- tabby daemon -session $0
  |     owns /tmp/tabby-daemon-0.sock
  |     writes tmux option @tabby_sidebar
  |
  +-- window @1
  |     +-- tabby render sidebar      (sidebar pane)
  |     +-- tabby render pane-header  (per content pane)
  |
  +-- window @2
        +-- tabby render sidebar
        +-- tabby render pane-header
```

### Daemon

One per session, started by `tabby.tmux` before the sidebar pane exists. It is
the only process that decides what the sidebar should say. It watches tmux for
window and pane changes, applies config and theme, renders the sidebar text,
and stores the result in the tmux option `@tabby_sidebar`.

It also listens on a Unix socket at `/tmp/tabby-daemon-$N.sock`. Renderers
connect to it and push input events back.

### Renderers

`tabby render sidebar` runs in the sidebar pane. It reads `@tabby_sidebar`,
prints it, and forwards clicks to the daemon over the socket. It holds no
state of its own, so killing it loses nothing.

`tabby render pane-header` runs in each content pane's header. Same idea at a
smaller scale.

The other render kinds are `window-header`, `sidebar-popup`, and
`pet-qa-popup`.

### Watchdog

`tabby watchdog` supervises the daemon and restarts it if it exits
unexpectedly. It stays down when the daemon exits cleanly on request, and
stays up when an external `SIGTERM` arrives. Turn it off with
`@tabby_watchdog off`.

## State

The tmux option `@tabby_sidebar` is the source of truth for what is on screen.
Everything else is derived from it or feeds into it.

That choice has a few consequences worth knowing:

- Renderers are disposable. Kill one and the next render repaints it correctly.
- State survives a renderer crash, because tmux holds it, not the process.
- Anything that can run `tmux show-options` can read the current sidebar.

Longer-lived state lives on disk:

| Path | Holds |
|---|---|
| `~/.config/tabby/config.yaml` | Your configuration |
| `~/.local/state/tabby/pet.json` | Pet widget memory |
| `~/.local/state/tabby/thought_buffer.txt` | Pet thought buffer |
| `/tmp/tabby-*` | Sockets, PID files, logs. Cleared on reboot. |

Per-window and per-pane facts are stored as tmux options on the window or
pane, so they follow the object around and vanish with it. The full list is in
[tmux Options](tmux-Options.md).

## Startup

`tabby.tmux` is 771 lines of shell and runs in two phases. The split exists
because tmux blocks while a plugin loads, and a slow plugin is felt as a slow
tmux.

**Phase 1 is synchronous** and takes about 20 milliseconds. It starts the
daemon and splits off the sidebar pane. That is all. By the time tmux hands
control back to you, the sidebar is visible.

**Phase 2 is asynchronous.** Phase 1 re-invokes `tabby.tmux` with
`TABBY_DEFERRED=1` set and returns immediately. The re-entry then makes the
106 remaining tmux calls: hooks, key bindings, and options. These arrive over
the following moment while you are already typing.

Sessions named `_tabby_*` are skipped entirely. Those are Tabby's own holding
sessions: `_tabby_minimized` for minimized windows, `_tabby_limbo` for windows
mid-move, and `_tabby_stash_*` for saved layouts.

Startup also sets three tmux options that Tabby depends on:

```
automatic-rename on
allow-rename on
automatic-rename-format '#{pane_current_command}'
```

That is why windows name themselves after the running command until you lock a
name with `prefix + r`.

## Input path

A click in the sidebar travels:

```
mouse click
  -> sidebar renderer decodes coordinates
  -> SEND over the unix socket
  -> daemon maps (x, y) to a region
  -> daemon runs the action (select window, toggle group, ...)
  -> daemon re-renders and writes @tabby_sidebar
  -> every renderer repaints
```

Each arrow has a log line when `@tabby_input_log` is on, which is what makes
click problems tractable. See [Troubleshooting](Troubleshooting.md).

## Packages

| Package | Responsibility |
|---|---|
| `pkg/colors` | The 15 themes and colour blending |
| `pkg/config` | YAML parsing, defaults, merge with tmux options |
| `pkg/tmux` | Talking to tmux, including sidebar width computation |

## Related

- [Development](Development.md) for building and testing
- [tmux Options](tmux-Options.md) for the state each process reads
- [CLI Reference](CLI-Reference.md) for every subcommand
