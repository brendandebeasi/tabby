# Development

## Requirements

- Go 1.24 or newer, matching the `go` directive in `go.mod`
- tmux 3.2 or newer
- GNU Make

## Build

```bash
git clone https://github.com/yourname/tabby ~/git/tabby
cd ~/git/tabby
make build
```

The binary lands at `bin/tabby`. Everything else is a symlink or a subcommand
of it.

To install into the plugin directory tmux reads:

```bash
make install
```

`PLUGIN_DIR` defaults to `~/.tmux/plugins/tabby`. Override it if you keep
plugins elsewhere:

```bash
make install PLUGIN_DIR=~/.config/tmux/plugins/tabby
```

If you build inside a container or VM that shares this directory with a Mac,
the resulting `bin/tabby` will be a Linux ELF and macOS will refuse to run it,
which surfaces as `returned 126`. Rebuild natively afterwards.

## Make targets

| Target | What it does |
|---|---|
| `build` | Build `bin/tabby` for the host platform |
| `build-linux` | Cross-compile Linux amd64 binaries into `bin-linux/`, for copying to a remote host. Not the way to build on Linux — use `build` for that, whatever platform you are on |
| `deps` | Download and tidy modules |
| `test` | Unit and integration tests |
| `test-unit` | `go test ./pkg/...` only |
| `test-e2e` | End-to-end tmux tests |
| `test-race` | Unit tests under the race detector |
| `test-cover` | Coverage report |
| `vet` | `go vet` |
| `ci` | What CI runs: vet, tests, build |
| `capture-visual` | Capture current visual output |
| `compare-visual` | Diff visual output against the baseline |
| `update-baseline` | Accept current visual output as the baseline |
| `install` | Build and copy into `PLUGIN_DIR` |
| `sync` | Install and reload the running tmux session |
| `dev` | Build and reload without a full install |
| `clean` | Remove build artifacts |
| `clean-all` | Also remove caches and captures |
| `help` | List targets |

`make sync` and `make dev` restart daemons in the session you are sitting in.
That is convenient on a scratch session and disruptive on a session you care
about.

## Tests

Unit tests:

```bash
go test ./pkg/...
```

Integration, which drives a real tmux server:

```bash
./tests/integration/tmux_test.sh
```

End-to-end, one scenario at a time:

```bash
./tests/e2e/run_e2e.sh sidebar_toggle_open
./tests/e2e/run_e2e.sh window_close_removes
./tests/e2e/run_e2e.sh stale_renderer_recovery
./tests/e2e/run_e2e.sh split_spawns_pane_header
```

Visual capture, which renders the sidebar and diffs it against a stored
baseline:

```bash
./tests/visual/capture_test.sh
```

When a visual diff is an intended change, accept it with `make update-baseline`
and commit the new baseline alongside the code that changed it.

Everything in a clean container:

```bash
docker build -t tabby-test -f tests/Dockerfile .
docker run --rm tabby-test
```

## Dev reload loop

Rebuilding does not by itself replace the daemon a session is already running.
Opt the session in first:

```bash
tmux set-option -g @tabby_dev_reload_enabled on
```

Then, after a build:

```bash
./bin/tabby dev reload
```

Check what version a session is running:

```bash
./bin/tabby dev status
```

`STALE` means the daemon predates the binary on disk.

Two environment variables help when scripting this:

```bash
TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET=dev ./bin/tabby dev reload
```

`TABBY_SKIP_BUILD=1` stops the script rebuilding first. `TABBY_SESSION_TARGET`
picks the session to act on instead of the current one.

## Git hooks

```bash
./scripts/install-git-hooks.sh
```

This installs a pre-commit hook that runs `make vet` and the unit tests.

## Dependencies

Tabby renders with lipgloss. **Stay on lipgloss v1.** v2 is pre-release and its
API is still moving; upgrading will break rendering in ways the tests do not
all catch.

## Layout

| Path | Contents |
|---|---|
| `cmd/` | Subcommand entry points |
| `pkg/colors/` | Themes and colour blending |
| `pkg/config/` | Config file parsing and defaults |
| `pkg/tmux/` | tmux interaction and layout maths |
| `tabby.tmux` | Plugin entry point, run by TPM |
| `tests/` | Integration, e2e, and visual tests |
| `scripts/` | Dev and install helpers |
| `docs/` | Prose docs, including this wiki |

## Related

- [Architecture](Architecture.md) for how the pieces fit together
- [Troubleshooting](Troubleshooting.md) for diagnosing a running session
