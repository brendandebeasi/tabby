# tmux Options Reference

Tabby reads its runtime settings from tmux user options. Anything you would
put in `~/.config/tabby/config.yaml` can also be set with `tmux set-option`,
and the tmux option wins when both are present. That makes options the right
tool for temporary changes and for per-session overrides.

Set an option globally so it survives new sessions:

```bash
tmux set-option -g @tabby_sidebar_width 32
```

Set one for the current session only:

```bash
tmux set-option @tabby_sidebar_width 32
```

Read one back:

```bash
tmux show-options -gqv @tabby_sidebar_width
```

Most options take effect on the next render, which happens within a few
hundred milliseconds. A few, listed below, are read once at plugin load and
need `prefix + I` (TPM reload) or a fresh session.

## Settings you set

These are the options meant for your `.tmux.conf`.

### Core

| Option | Values | Default | Effect |
|---|---|---|---|
| `@tabby_enabled` | `on` / `off` | `on` | Master switch. `off` stops Tabby taking over the session at load. Read at load time. |
| `@tabby_sidebar_position` | `left` / `right` | `left` | Which side the sidebar pane sits on. |
| `@tabby_sidebar_mode` | `windows` / `panes` / `both` | `both` | What the sidebar lists. |
| `@tabby_pane_headers` | `on` / `off` | `on` | Per-pane header bars. See [Pane Headers](Pane-Headers.md). |
| `@tabby_terminal_bg` | hex colour | unset | Tells Tabby your terminal's background so blended colours match. See [Themes](Themes.md). |
| `@tabby_base_index` | integer | tmux `base-index` | Index the first window uses, when you want it to differ from tmux's own. |

### Sidebar width

The responsive rules behind these are in [Responsive Layout](Responsive-Layout.md).

| Option | Default | Effect |
|---|---|---|
| `@tabby_sidebar_width` | `28` | Width used when no responsive profile applies. |
| `@tabby_sidebar_width_desktop` | `28` | Width on wide windows. Rejected below 15. |
| `@tabby_sidebar_width_tablet` | `22` | Width on medium windows. Rejected below 15. |
| `@tabby_sidebar_width_mobile` | `18` | Width on narrow windows. Floored at 10. |
| `@tabby_sidebar_width_mobile_keyboard` | `14` | Width when a soft keyboard has shortened the window. |
| `@tabby_sidebar_mobile_max_window_cols` | `110` | Windows this wide or narrower use the mobile profile. Only accepted at 60 or above. |
| `@tabby_sidebar_tablet_max_window_cols` | `170` | Windows this wide or narrower use the tablet profile. Must be at least the mobile value. |
| `@tabby_sidebar_mobile_max_percent` | `40` | Ceiling on the mobile sidebar as a share of window width. Applies to mobile and tablet windows only. |
| `@tabby_sidebar_desktop_max_percent` | `50` | Ceiling on the sidebar as a share of window width on desktop windows. Accepted between 10 and 80, and never below the mobile ceiling. |
| `@tabby_sidebar_mobile_min_content_cols` | `40` | Columns always left over for your content pane on mobile. |
| `@tabby_sidebar_mobile_keyboard_rows` | `20` | Window height at or below which Tabby assumes a soft keyboard is up. |
| `@tabby_sidebar_min_width` | `10` | Hard floor for any computed width. |

### Behaviour

| Option | Values | Default | Effect |
|---|---|---|---|
| `@tabby_prefix_mode` | `on` / `off` | `off` | Show a prefix indicator in the sidebar while the prefix key is held. |
| `@tabby_border_from_tab` | `on` / `off` | `on` | Tint pane borders with the window's group colour. |
| `@tabby_tint_bg` | `on` / `off` | `off` | Tint the sidebar background with the active group colour. |
| `@tabby_enable_focus_repair` | `on` / `off` | `on` | Restore focus to the content pane when tmux leaves it on the sidebar. |
| `@tabby_enable_orphan_window_kill` | `on` / `off` | `on` | Clean up windows left holding only a Tabby helper pane. |
| `@tabby_client_idle_timeout_hours` | integer | `24` | Hours before an idle detached client's state is dropped. |
| `@tabby_close_select_window` | window id | unset | Window to select after the next close, instead of tmux's choice. |
| `@tabby_close_select_index` | integer | unset | Same, by index. |

### Appearance

| Option | Values | Effect |
|---|---|---|
| `@tabby_appearance_auto` | `on` / `off` | Follow the OS light/dark setting. |
| `@tabby_appearance_key` | string | Key Tabby watches for the OS appearance value. |
| `@tabby_pane_active_fg` | colour | Text colour in the active pane header. |
| `@tabby_pane_inactive_fg` | colour | Text colour in inactive pane headers. |
| `@tabby_pane_active_bg_default` | colour | Fallback background for the active pane header. |
| `@tabby_pane_inactive_bg_default` | colour | Fallback background for inactive pane headers. |
| `@tabby_pane_command_fg` | colour | Colour of the command name in a pane header. |
| `@tabby_pane_fg` | colour | Base pane header foreground. |
| `@tabby_pane_fg_dim` | colour | Dimmed pane header foreground. |
| `@tabby_dash_bg_active` | colour | Dashboard active row background. |
| `@tabby_dash_bg_inactive` | colour | Dashboard inactive row background. |
| `@tabby_dash_layout` | tmux layout name | Dashboard arrangement. One of `tiled`, `even-horizontal`, `even-vertical`, `main-vertical`, `main-horizontal`, `main-vertical-auto`, `main-horizontal-auto`. See [Dashboard](Dashboard.md). |

### Diagnostics

| Option | Values | Default | Effect |
|---|---|---|---|
| `@tabby_input_log` | `on` / `off` | `off` | Log every click and keypress. Cached for 10 seconds before a change is picked up. See [Troubleshooting](Troubleshooting.md). |
| `@tabby_dev_reload_enabled` | `on` / `off` | `off` | Let `tabby dev reload` restart daemons in place. See [Development](Development.md). |
| `@tabby_watchdog` | `on` / `off` | `on` | Supervise the daemon and restart it if it dies. |

## Settings Tabby sets

Tabby writes these itself to hold state. You can read them, and setting a few
of them by hand is a documented way to script Tabby, but most are internal and
will be overwritten on the next render.

### Per window

| Option | Meaning |
|---|---|
| `@tabby_group` | Group name this window belongs to. Safe to set; see [Groups and Colors](Groups-and-Colors.md). |
| `@tabby_color` | Group colour override for this window. Safe to set. |
| `@tabby_icon` | Icon shown next to the window name. Safe to set. |
| `@tabby_name_locked` | `1` when you renamed the window and Tabby should stop auto-renaming it. |
| `@tabby_locked` | Window is pinned against automatic cleanup. |
| `@tabby_pinned` | Window is pinned to the top of the sidebar. |
| `@tabby_collapsed` | Window's pane list is collapsed in the sidebar. |
| `@tabby_busy` | A long-running command is active in this window. |
| `@tabby_bell` | Bell fired since you last looked. |
| `@tabby_activity` | Output arrived since you last looked. |
| `@tabby_silence` | Window has been quiet past the silence threshold. |
| `@tabby_input` | Window is waiting on input. |
| `@tabby_ai_title` | Title an AI tool reported for this window. See [AI Tool Indicators](AI-Tool-Indicators.md). |
| `@tabby_color_seeded` | Colour was auto-assigned rather than chosen. |
| `@tabby_crash` | Last helper process for this window crashed. |
| `@tabby_remote_cwd` | Working directory reported over SSH. See [SSH and Remote Hosts](SSH-and-Remote-Hosts.md). |

### Per pane

| Option | Meaning |
|---|---|
| `@tabby_pane_title` | Title shown in the pane header. Safe to set. |
| `@tabby_pane_active` | Marks the pane Tabby considers active. |
| `@tabby_pane_collapsed` | Pane is collapsed to its header. |
| `@tabby_pane_dim` | Pane is dimmed because it is inactive. |
| `@tabby_pane_prev_height` | Height to restore a collapsed pane to. |
| `@tabby_prompt_icon` | Icon the shell prompt integration reported. |

### Per session

| Option | Meaning |
|---|---|
| `@tabby_sidebar` | The rendered sidebar state. This is the source of truth every renderer reads. |
| `@tabby_daemon_pid` | PID of this session's daemon. |
| `@tabby_sidebar_collapsed` | Sidebar is collapsed to a narrow strip. |
| `@tabby_sidebar_previous_width` | Width to restore on expand. |
| `@tabby_stashed_width` | Width saved before a fullscreen or minimize. |
| `@tabby_sync_width` | Width being propagated to other windows. |
| `@tabby_collapsed_groups` | Comma-separated list of collapsed group names. |
| `@tabby_grp_collapsed_<name>` | Per-group collapse flag. |
| `@tabby_dashboard` | Dashboard is open. |
| `@tabby_minimized` | Session is in the minimized holding state. |
| `@tabby_fullscreen_sidebar` | Sidebar is in fullscreen mode. |
| `@tabby_fs_origin`, `@tabby_fs_footer_height` | Where to return from fullscreen. |
| `@tabby_dash_origin`, `@tabby_dash_origin_color`, `@tabby_dash_origin_icon`, `@tabby_dash_origin_name` | Where to return from the dashboard. |
| `@tabby_min_origin`, `@tabby_min_dir`, `@tabby_min_host`, `@tabby_min_placeholder` | Where to return from minimize. |
| `@tabby_last_window`, `@tabby_last_pane` | Previous selection, for toggling back. |
| `@tabby_last_click_pane`, `@tabby_last_click_x`, `@tabby_last_click_y` | Coordinates of the last sidebar click. |
| `@tabby_spawning` | A helper pane is mid-spawn. Suppresses cleanup. |
| `@tabby_layout_<id>` | Saved layout for a window. |
| `@tabby_skip_preserve_<id>` | Skip layout preservation for one window. |

## Environment variables

Options cover behaviour. These cover paths, and they are read from the
environment Tabby's processes inherit.

| Variable | Default | Purpose |
|---|---|---|
| `TABBY_CONFIG_DIR` | `~/.config/tabby` | Where `config.yaml` lives. |
| `TABBY_STATE_DIR` | `~/.local/state/tabby` | Where `pet.json` and `thought_buffer.txt` live. |
| `TABBY_DIR` | `~/.tmux/plugins/tabby` | Plugin root, used to find `bin/tabby`. |
| `TABBY_RUNTIME_PREFIX` | `/tmp/tabby-` | Prefix for sockets, PID files, and logs. |
| `TABBY_SKIP_BUILD` | unset | Set to `1` to stop dev scripts rebuilding before they run. |
| `TABBY_SESSION_TARGET` | unset | Session the dev scripts should act on. |
| `TABBY_DEFERRED` | set by Tabby | Marks the second, asynchronous half of plugin init. Do not set by hand. |

## Related

- [Configuration](Configuration.md) for the YAML file and how it merges with these options
- [Responsive Layout](Responsive-Layout.md) for how the width options interact
- [Troubleshooting](Troubleshooting.md) for what to do when an option looks ignored
