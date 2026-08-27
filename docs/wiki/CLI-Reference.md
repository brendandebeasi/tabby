[Home](Home.md) › CLI Reference

# CLI Reference

The binary is at `~/.tmux/plugins/tabby/bin/tabby`. Add `bin/` to your `PATH`,
or call it by full path from scripts and hooks.

```
Usage: tabby <subcommand> [args...]
```

## Commands you will use

| Command | What it does |
|---|---|
| `tabby toggle` | Enable or disable Tabby for this session. Bound to `prefix + Tab`. |
| `tabby theme [toggle\|light\|dark]` | Show or change the light/dark selection. Bound to `prefix + T`. |
| `tabby new-window [name]` | New window inheriting the group's working directory and colour. |
| `tabby dashboard` | Toggle the all-panes dashboard, gathering panes into a tiled grid. Bound to `prefix + 0` and `prefix + g`. See [Dashboard](Dashboard.md). |
| `tabby dashboard-layout` | Open the dashboard layout picker. Bound to `prefix + L`. See [Dashboard](Dashboard.md). |
| `tabby pane-picker` | Interactive pane picker for keyboard-driven selection. |
| `tabby manage-group` | TUI for editing group entries in `config.yaml`. |
| `tabby setup` | First-run configuration wizard. |
| `tabby cycle-pane` | Cycle the active content pane, skipping sidebar and headers. |
| `tabby clip send` | Push text up to the clipboard of the device you are sitting at. Bound to `prefix + Y`. |
| `tabby pet <ask\|traits\|forget>` | Interact with the pet widget. |

### theme

```
tabby theme          # show the current selection
tabby theme toggle   # switch between the configured light and dark themes
tabby theme light    # select the light theme
tabby theme dark     # select the dark theme
```

The choice is written back to `config.yaml`, so it survives a restart. See
[Themes](Themes.md).

### cycle-pane

```
tabby cycle-pane
tabby cycle-pane --ensure-content   # move focus to a content pane only if a
                                    # sidebar or header pane is active
tabby cycle-pane --dim-only         # just re-apply inactive-pane dimming
```

`--ensure-content` is what the window-switch hooks call, so you never land on
the sidebar after switching windows.

### clip

```
tabby clip send                      # read stdin
tabby clip send --text 'hello'       # send a literal string
tabby clip send --file ./report.txt  # send a file's contents
tabby clip send --pane               # send this pane's last 100 lines
tabby clip send --pane %3 --lines 300
```

Options: `--tty PATH` to write the escape somewhere other than the pane's pty,
`--max N` to change the 64 KiB cap, `--passthrough` for a nested remote tmux,
`--quiet` to suppress the confirmation.

Text only, one direction: up. See
[SSH and Remote Hosts](SSH-and-Remote-Hosts.md#clipboard) for the mosh and iOS
setup, and for `scripts/tabby-clip.sh`, which does the same thing in pure shell
on hosts without the binary.

### pet

```
tabby pet ask                    # print the pet's pending question, if any
tabby pet ask --answer "..."     # answer it
tabby pet traits                 # list what the pet has learned about you
tabby pet forget <id>            # remove an answer and anything derived from it
```

See [Widgets](Widgets.md#pet).

## Hooks

`tabby hook` dispatches the actions tmux bindings and hooks call. Two are worth
knowing by hand:

```bash
tabby hook focus-pane main:2.1          # jump to a session, window and pane
tabby hook toggle-collapse-sidebar      # collapse or restore the sidebar
```

`focus-pane` is the deep-link target for notifications; see
[Notifications and Deep Links](Notifications-and-Deep-Links.md). It accepts
`2`, `1.2` or `main:2.1`.

`set-indicator` is the other one you will call from scripts:

```bash
tabby hook set-indicator busy 1
tabby hook set-indicator input 1
tabby hook set-indicator bell 1
```

See [AI Tool Indicators](AI-Tool-Indicators.md).

The rest are registered by `tabby.tmux` and are not meant to be run by hand:
`ensure-sidebar`, `on-pane-resize`, `preserve-pane-ratios`, `kill-pane`,
`kill-window`, `split-pane`, `new-group`, `next-window`, `prev-window`,
`pane-menu`, `toggle-minimize-window`, `osc-handler`, `resurrect-save`,
`resurrect-restore`.

## Developer commands

| Command | What it does |
|---|---|
| `tabby dev status` | Report whether this session's daemon matches the current build |
| `tabby dev reload` | Rebuild and restart the runtime |

`dev reload` needs to be enabled once per tmux server:

```bash
tmux set-option -g @tabby_dev_reload_enabled 1
```

It exits non-zero and posts a tmux message if the runtime is still stale
afterwards. See [Development](Development.md).

## Internal commands

Spawned by `tabby.tmux` and by the daemon. Running them by hand will not do what
you want.

| Command | Role |
|---|---|
| `tabby daemon` | Per-session socket server and coordinator |
| `tabby watchdog` | Supervises the daemon, restarting it on crash |
| `tabby render <kind>` | Renderer processes: `sidebar`, `window-header`, `pane-header`, `sidebar-popup`, `pet-qa-popup`, `degraded-models-popup`, `close-confirm`, `dash-layout-popup` |

## Environment variables

| Variable | Effect |
|---|---|
| `TABBY_CONFIG_DIR` | Directory holding `config.yaml`, default `~/.config/tabby` |
| `TABBY_STATE_DIR` | State directory, default `~/.local/state/tabby` |
| `TABBY_DIR` | Plugin install directory |
| `TABBY_RUNTIME_PREFIX` | Prefixes socket and pid file names, isolating a test or demo daemon |
| `TABBY_TEAMCLAUDE_API_KEY` | Key for the teamclaude widget |
| `TABBY_KIMI_URL`, `TABBY_KIMI_API_KEY` | Endpoint and key for the Kimi widget |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` | Used by the pet's thought bubbles |
| `TABBY_DEBUG` | Verbose logging |
| `TABBY_PERF`, `TABBY_RENDER_TRACE` | Performance and render tracing |
