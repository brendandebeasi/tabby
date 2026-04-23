# Tabby demo harness

Isolated environment for demonstrating tabby features without touching your
live tmux session. Uses `TABBY_RUNTIME_PREFIX`, a private tmux socket, an
isolated config dir, and a PATH shim so the spawned daemon's `tmux`
subprocesses hit only the test server.

## Layout

- `lib/harness.sh` — setup/teardown, `ss_tmux` wrapper, helpers for adding
  fake windows, tagging groups, pinning, minimizing, and poking the daemon
  to refresh.
- `lib/narrate.sh` — boxed captions + inline sidebar snapshots for
  narrated-driver scenarios.
- `scenarios/01-minimize-driver.sh` — end-to-end walk-through of the
  minimize feature: groups, minimize, cmd+]/cmd+[ skipping, unminimize.

## Running a scenario

```
bash tests/demos/scenarios/01-minimize-driver.sh
```

Output scrolls through captions interleaved with sidebar snapshots. Colors
and the dim-on-minimize are preserved in the ANSI output.

## Recording

Automated recording via vhs/asciinema+agg currently renders blank GIFs on
this Mac — their ttyd+Chrome-headless pipeline is flaky here. The scenarios
work perfectly when piped to a real TTY, so for now:

- Run the scenario directly in your terminal and capture with your usual
  screen recorder (macOS Cmd+Shift+5, iTerm Export Buffer, etc.).
- Or `asciinema rec demo.cast` from your own live shell before launching
  the scenario — your shell has a real PTY so capture works end-to-end.

On a Linux CI box the vhs / asciinema paths should just work; both tools
are standard and the scripts don't depend on anything macOS-specific.

## Isolation guarantees

- tmux: fresh socket at `-L tabbyss-<pid>`. Your live `tmux` is untouched.
- tabby daemon: listens on `/tmp/<prefix>tabby-daemon-*.sock` with a
  per-run prefix. Your live daemon is untouched.
- config: `TABBY_CONFIG_DIR` redirected to the run's temp dir. A fixed
  demo config (StudioDome / Gunpowder / Default groups) is written there
  so visuals are reproducible regardless of your `~/.config/tabby/config.yaml`.
- state: `TABBY_STATE_DIR` redirected similarly.

All four are torn down on exit via a trap.
