# Tabby Project Guidelines

## Config & State Paths

- Config: `~/.config/tabby/config.yaml` (env override: `TABBY_CONFIG_DIR`)
- State: `~/.local/state/tabby/` (env override: `TABBY_STATE_DIR`)
- Runtime: `/tmp/tabby-*`

Go code resolves paths via `pkg/paths/paths.go`. Shell scripts must source `scripts/_config_path.sh` and use `$TABBY_CONFIG_FILE` -- never use `$CURRENT_DIR/config.yaml` directly.

## Process Management

When working with the daemon-based sidebar architecture:

- Don't forget which process you should be talking to when coding
- Don't forget to kill off old processes before testing new builds
- Make sure you know which process, window, pane you are trying to target
- Be careful to verify the correct processes are running after restarts

Use `pkill -f "tabby-daemon"` and `pkill -f "sidebar-renderer"` to clean up before testing.

## Architecture

The sidebar has two modes:
- Old: Multiple independent sidebar processes (one per window)
- New: Single daemon + lightweight renderers (TABBY_USE_RENDERER=1)

## Building

Always build directly to `bin/` directory (scripts expect binaries there):
```bash
go build -o bin/tabby-daemon ./cmd/tabby-daemon
go build -o bin/sidebar-renderer ./cmd/sidebar-renderer
```

Do NOT build to the root directory (`./tabby-daemon`) - the toggle scripts use `bin/`.

## Restarting Sidebar

After rebuilding, restart the sidebar with:
```bash
pkill -f "tabby-daemon"; pkill -f "sidebar-renderer"; rm -f /tmp/tabby-daemon-*
TABBY_USE_RENDERER=1 /Users/b/git/tabby/scripts/toggle_sidebar_daemon.sh
```
