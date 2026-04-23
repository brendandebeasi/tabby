# Tabby demo harness

Isolated environment for recording tabby feature demos without touching your
live tmux session. Uses `TABBY_RUNTIME_PREFIX`, a private tmux socket, an
isolated config dir, and a PATH shim so the spawned daemon's `tmux`
subprocesses hit only the test server.

## Layout

- `lib/harness.sh` — setup/teardown, `ss_tmux` wrapper, helpers for adding
  fake windows, tagging groups, pinning, minimizing, and poking the daemon
  to refresh.
- `lib/narrate.sh` — caption + boxed sidebar-snapshot helpers for
  narrated-driver scenarios.
- `scenarios/<name>-driver.sh` — end-to-end walk-through of a feature.
  Each driver prints captions interleaved with live sidebar snapshots.
- `record.sh` — thin wrapper that pipes a driver through
  `asciinema rec --headless` + `agg` to produce `.cast` and `.gif` files
  in `out/`.

## Running a scenario

Direct (just see the output):

```
bash tests/demos/scenarios/01-minimize-driver.sh
```

Record + convert to gif:

```
tests/demos/record.sh 01-minimize        # one scenario
tests/demos/record.sh all                # every *-driver.sh in scenarios/
```

Outputs land in `tests/demos/out/<name>.cast` and `tests/demos/out/<name>.gif`.

Tuning knobs (env overrides for `record.sh`):

| Var            | Default  | Meaning                       |
| -------------- | -------- | ----------------------------- |
| `ASC_COLS`     | 80       | asciinema terminal width      |
| `ASC_ROWS`     | 60       | asciinema terminal height     |
| `AGG_FONT_SIZE`| 13       | gif font size                 |
| `AGG_THEME`    | monokai  | agg theme name                |
| `AGG_FPS`      | 15       | gif frame-rate cap            |

## Why the recorder works

The driver scripts never call `tmux attach`. They drive the isolated tmux
server from the outside and grab the sidebar's current state via
`tmux capture-pane -p -e`, then print that (ANSI-intact) to stdout. That
plain stdout stream is what `asciinema` records and `agg` renders — no
alt-screen buffers, no terminal-query round-trips, no Chrome-headless in
the loop. Flakiness we saw with vhs and with `asciinema rec -c 'tmux attach'`
is avoided entirely.

## Isolation guarantees

- **tmux**: fresh socket at `-L tabbyss-<nonce>`. Your live `tmux` is
  untouched.
- **tabby daemon**: listens on `/tmp/<prefix>tabby-daemon-*.sock` with a
  per-run prefix. Your live daemon is untouched.
- **config**: `TABBY_CONFIG_DIR` redirected to the run's temp dir. A fixed
  demo config (StudioDome / Gunpowder / Default groups) is written there
  so visuals are reproducible regardless of your
  `~/.config/tabby/config.yaml`.
- **state**: `TABBY_STATE_DIR` redirected similarly.

All four are torn down on exit via a trap.

## Adding a new scenario

Copy `scenarios/01-minimize-driver.sh` as a template. Each driver:

1. Sources the harness and narrate libs.
2. Calls `ss_setup <cols> <rows>` and populates windows / groups /
   indicators via the `ss_*` helpers.
3. Uses `narr_caption "..."` + `narr_snapshot "$(ss_sidebar_pane)"` +
   `narr_pause 1.5` to build up a sequence of annotated frames.
4. Drives state changes (select window, set minimize, etc.), calling
   `ss_poke_daemon` after each change so the sidebar snapshot reflects
   it promptly.

Drop the file in `scenarios/` with the `-driver.sh` suffix and
`record.sh all` will pick it up automatically.
