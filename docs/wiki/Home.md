# Tabby Wiki

Tabby is a tab manager for tmux: a clickable vertical sidebar, colour-coded
window groups, per-pane headers, sidebar widgets, and notifications that
deep-link back to the exact pane that sent them.

<table>
<tr>
<td align="center"><b>Sidebar on a wide terminal</b><br/>Windows, groups, and widgets down the left edge.</td>
<td align="center"><b>Mobile layout</b><br/>Under 110 columns the sidebar drops to icons.</td>
</tr>
<tr>
<td width="72%"><img src="../../screenshots/main.png" alt="Tabby desktop view"/></td>
<td width="28%"><img src="../../screenshots/mobile.png" alt="Tabby mobile view"/></td>
</tr>
</table>

## Getting started

| Page | What it covers |
|---|---|
| [Installation](Installation.md) | TPM and manual install, requirements, first run |
| [Quick Start](Quick-Start.md) | The five things to try in your first ten minutes |
| [Keyboard and Mouse](Keyboard-and-Mouse.md) | Every binding, every context menu |

## Configuration

| Page | What it covers |
|---|---|
| [Configuration](Configuration.md) | Where `config.yaml` lives and how it is structured |
| [Groups and Colors](Groups-and-Colors.md) | Pattern matching, per-group themes, custom tab colours |
| [Themes](Themes.md) | The 14 built-in presets and the light/dark toggle |
| [Sidebar](Sidebar.md) | Position, mode, collapse, sort order, tree glyphs |
| [Pane Headers](Pane-Headers.md) | Titles, dimming, borders, drag and resize controls |
| [Dashboard](Dashboard.md) | Gather every pane into one tiled window and back again |
| [Widgets](Widgets.md) | Clock, pet, stats, git, session, Claude and Kimi quota |
| [Responsive Layout](Responsive-Layout.md) | Mobile, tablet and desktop profiles; width sync |
| [tmux Options](tmux-Options.md) | Every `@tabby_*` option and environment variable |

## Integrations

| Page | What it covers |
|---|---|
| [Notifications and Deep Links](Notifications-and-Deep-Links.md) | Click a notification, land on the pane |
| [AI Tool Indicators](AI-Tool-Indicators.md) | Claude Code, OpenCode, Grok, and passive detection |
| [SSH and Remote Hosts](SSH-and-Remote-Hosts.md) | Remote host colours, bell notifications, mosh limits |
| [Session Persistence](Session-Persistence.md) | tmux-resurrect save and restore |

## Reference and support

| Page | What it covers |
|---|---|
| [CLI Reference](CLI-Reference.md) | Every user-facing `tabby` subcommand |
| [Troubleshooting](Troubleshooting.md) | Symptoms, causes, and log files |
| [Architecture](Architecture.md) | Daemon, renderers, and how state is stored |
| [Development](Development.md) | Building, testing, and the dev reload loop |

## Where things live

| Category | Path | Env override |
|---|---|---|
| Config | `~/.config/tabby/config.yaml` | `TABBY_CONFIG_DIR` |
| State (pet, caches) | `~/.local/state/tabby/` | `TABBY_STATE_DIR` |
| Runtime sockets and logs | `/tmp/tabby-*` | `TABBY_RUNTIME_PREFIX` |
| Plugin install | `~/.tmux/plugins/tabby` | `TABBY_DIR` |

## Related projects

- [tabby-zj](https://github.com/brendandebeasi/tabby-zj) ports Tabby to Zellij
  as a single Rust WASM plugin, with the same sidebar, menus, indicators and
  widgets, and no tmux.
