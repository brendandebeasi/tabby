# Troubleshooting

Start here when something looks wrong. Each symptom below lists what usually
causes it and how to confirm.

## First checks

Two commands answer most questions before you go further.

```bash
~/.tmux/plugins/tabby/bin/tabby dev status
```

If it prints `STALE`, the daemon running your session is an older binary than
the one on disk. Restart it by toggling the sidebar off and on:

```
prefix + Tab
prefix + Tab
```

```bash
file ~/.tmux/plugins/tabby/bin/tabby
```

On macOS this must say `Mach-O 64-bit executable arm64` (or `x86_64`). If it
says `ELF 64-bit LSB executable`, the binary was built for Linux, likely inside
a container or VM sharing the directory. Rebuild:

```bash
cd ~/.tmux/plugins/tabby && make build
```

A Linux binary on macOS shows up as `returned 126` errors from tmux hooks.

## Sidebar problems

### No sidebar at all

Check the plugin loaded:

```bash
tmux show-options -gqv @tabby_enabled
```

Empty output means `tabby.tmux` never ran. Confirm TPM installed it and that
`set -g @plugin 'yourname/tabby'` is in `~/.tmux.conf` above `run '~/.tmux/plugins/tpm/tpm'`.

If it prints `on` and there is still no sidebar, the daemon may not have
started. Look at the events log:

```bash
tail -50 /tmp/tabby-daemon-$(tmux display -p '#{session_id}' | tr -d '$')-events.log
```

Tabby also deliberately skips its own internal sessions. If your session is
named `_tabby_minimized`, `_tabby_limbo`, or `_tabby_stash_*`, that is a
holding session and will never get a sidebar.

### Sidebar is there but blank

The sidebar renderer reads the tmux option `@tabby_sidebar`. If the option is
empty, the daemon is not writing:

```bash
tmux show-options -qv @tabby_sidebar | head -5
```

Empty means the daemon is down or stuck. Check for a crash:

```bash
cat /tmp/tabby-daemon-*-crash.log
```

### Clicks do nothing

Enable input logging, click once, then read both sides:

```bash
tmux set-option -g @tabby_input_log on
tail -f /tmp/tabby-daemon-*-input.log /tmp/sidebar-renderer-*-input.log
```

The setting is cached for 10 seconds, so wait before clicking.

Read the result this way:

| What you see | What it means |
|---|---|
| No `SEND` line | The renderer never saw the click. tmux mouse mode is probably off: `tmux set -g mouse on`. |
| `SEND_FAILED not connected` | The renderer lost its socket to the daemon. Toggle the sidebar twice. |
| `SEND` but no `INPUT` | The message left the renderer and never arrived. Check the daemon crash log. |
| `CLICK_MISS` | The click landed on empty sidebar space, not a row. |
| `INPUT_SLOW` | Processing took over 50ms. Something is blocking the daemon. |
| `HEALTH clients=0` | No renderer is connected. |

Turn logging back off when you are done:

```bash
tmux set-option -g @tabby_input_log off
```

### Sidebar is the wrong width

Width is computed, not fixed. See [Responsive Layout](Responsive-Layout.md)
for the rules. Common surprises:

- On a window 110 columns or narrower, the mobile profile applies and the width
  is also capped at 40 percent of the window and floored so 40 columns stay for
  your content.
- `@tabby_sidebar_width_desktop` and `@tabby_sidebar_width_tablet` are rejected
  below 15, silently falling back to the default.
- `@tabby_sidebar_mobile_max_window_cols` below 60 is rejected.

Print what Tabby actually decided:

```bash
tmux display -p '#{window_width} #{pane_width}'
```

### Sidebar reappears after you close it

Closing the sidebar pane with `prefix + x` is not the same as toggling it off.
The daemon will respawn it. Use `prefix + Tab`.

## Colour and theme problems

### Colours look washed out or wrong against the background

Tabby blends some colours against what it believes your terminal background
is. Tell it explicitly:

```bash
tmux set -g @tabby_terminal_bg '#191724'
```

### Theme changes do not stick

`tabby theme dark` writes the selection but a `config.yaml` `theme:` key set
explicitly will win on the next full reload. Check which is set:

```bash
grep -n '^theme' ~/.config/tabby/config.yaml
~/.tmux/plugins/tabby/bin/tabby theme
```

### Only 8 or 16 colours render

Your terminal is not advertising truecolor. In `~/.tmux.conf`:

```bash
set -g default-terminal "tmux-256color"
set -ga terminal-overrides ",*256col*:Tc"
```

## Pane header problems

### Headers missing on some panes

Headers are per content pane and are skipped for Tabby's own helper panes.
Confirm they are enabled at all:

```bash
tmux show-options -gqv @tabby_pane_headers
```

### A header shows a stale title

Titles come from `@tabby_pane_title`, or from the running command when that is
unset. If an AI tool set `@tabby_ai_title` and then exited without clearing it,
clear it by hand:

```bash
tmux set-option -p -u @tabby_ai_title
```

## Window naming problems

### Windows keep renaming themselves

Tabby sets `automatic-rename on` with a format of `#{pane_current_command}`.
That is deliberate. To lock one window's name, rename it with `prefix + r`,
which also sets `@tabby_name_locked`.

### A renamed window reverted

The lock is per window and is cleared if the window is recreated. Check it:

```bash
tmux show-options -wqv @tabby_name_locked
```

## Daemon problems

### `returned 126` in tmux messages

The binary is not executable on this platform. See the `file` check above.

### `returned 127`

The binary is missing. tmux is looking at `$TABBY_DIR/bin/tabby`, defaulting to
`~/.tmux/plugins/tabby/bin/tabby`. Build it or fix `TABBY_DIR`.

### Daemon restarts in a loop

Read the crash log for the panic:

```bash
cat /tmp/tabby-daemon-*-crash.log
```

To stop the watchdog while you investigate:

```bash
tmux set-option -g @tabby_watchdog off
```

### Two daemons for one session

Only one should hold the socket. Check the recorded PID against what is
running:

```bash
tmux show-options -qv @tabby_daemon_pid
pgrep -fl 'tabby daemon'
```

Kill the strays and toggle the sidebar twice.

## Log file reference

`$N` is the session id number, from `tmux display -p '#{session_id}'` without
the leading `$`. `@N` is a window id.

| File | Contents |
|---|---|
| `/tmp/tabby-daemon-$N.sock` | Unix socket the renderers connect to |
| `/tmp/tabby-daemon-$N.pid` | Running daemon's PID |
| `/tmp/tabby-daemon-$N-events.log` | Spawn, connect, cleanup, restart requests |
| `/tmp/tabby-daemon-$N-input.log` | Clicks and keys, when `@tabby_input_log` is on |
| `/tmp/tabby-daemon-$N-crash.log` | Panic stack traces |
| `/tmp/sidebar-renderer-@N-input.log` | Per-window click dispatch |
| `/tmp/sidebar-renderer-@N-crash.log` | Per-window panics |
| `/tmp/sidebar-renderer-@N-debug.log` | Verbose renderer output, `-debug` only |
| `/tmp/tabby-debug.log` | General sidebar debug output |

## Starting over

If nothing above helps, reset the session's Tabby state without restarting
tmux:

```bash
tmux set-option -gu @tabby_sidebar
pkill -f 'tabby daemon'
rm -f /tmp/tabby-daemon-*.sock /tmp/tabby-daemon-*.pid
```

Then toggle the sidebar with `prefix + Tab`. The daemon respawns and rebuilds
its state from tmux.

## Related

- [tmux Options](tmux-Options.md) for every option named here
- [Architecture](Architecture.md) for what each process does
- [Development](Development.md) for building and testing
