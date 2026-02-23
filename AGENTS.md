# AGENTS.md - Claude Code Guide

## Table of Contents

- [Project Overview](#project-overview)
- [Architecture](#architecture)
- [Config & State Paths](#config--state-paths)
- [State and Modes](#state-and-modes)
- [Process Management](#process-management)
- [Build and Install](#build-and-install)
- [Key Files](#key-files)
- [Testing and Verification](#testing-and-verification)
- [Common Issues](#common-issues)
- [Dependencies](#dependencies)

## Project Overview

Tabby is a tmux plugin with a daemon-driven vertical UI and optional horizontal UI.
The runtime center is `tabby-daemon`, with per-window `sidebar-renderer` clients and
per-pane `pane-header` clients.

## Architecture

```text
tabby/
├── cmd/
│   ├── tabby-daemon/        # Session coordinator + render payloads
│   ├── sidebar-renderer/    # Per-window sidebar TUI client
│   ├── pane-header/         # Per-pane header TUI client
│   ├── tabbar/              # Horizontal mode top pane UI
│   ├── pane-bar/            # Horizontal mode pane selector UI
│   ├── render-status/       # Native tmux status rendering helpers
│   ├── render-tab/
│   ├── render-pane-bar/
│   ├── manage-group/
│   └── tabby-web-bridge/
├── pkg/
│   ├── config/              # YAML config loading + schema structs
│   ├── daemon/              # Daemon protocol payloads
│   ├── grouping/            # Grouping + color logic
│   ├── paths/               # XDG config/state path resolution
│   ├── tmux/                # tmux wrappers (windows/panes/options)
│   └── perf/                # Runtime performance instrumentation
├── scripts/                 # tmux hooks, mode toggles, helper commands
├── tests/                   # integration/e2e/visual scripts
├── config.yaml              # Example/default config template
└── tabby.tmux               # Plugin entrypoint + hooks + bindings
```

The sidebar has two modes:
- Old: Multiple independent sidebar processes (one per window)
- New: Single daemon + lightweight renderers (`TABBY_USE_RENDERER=1`)

## Config & State Paths

- Config: `~/.config/tabby/config.yaml` (env override: `TABBY_CONFIG_DIR`)
- State: `~/.local/state/tabby/` (env override: `TABBY_STATE_DIR`)
- Runtime: `/tmp/tabby-*`

Go code resolves paths via `pkg/paths/paths.go`. Shell scripts must source `scripts/_config_path.sh` and use `$TABBY_CONFIG_FILE` -- never use `$CURRENT_DIR/config.yaml` directly.

## State and Modes

- Source-of-truth runtime state: tmux option `@tabby_sidebar`
- Values:
  - `enabled`: vertical sidebar mode
  - `horizontal`: top tabbar mode
  - `disabled`: native tmux status mode
- Per-session runtime files: `/tmp/tabby-daemon-<session>.{pid,sock,events.log,input.log}`

## Process Management

When working with the daemon-based sidebar architecture:

- Know which process you should be talking to before making changes
- Kill stale processes before testing new builds
- Know which process, window, and pane you are targeting
- Verify the correct processes are running after restarts

Clean up before testing:
```bash
pkill -f "tabby-daemon"; pkill -f "sidebar-renderer"; rm -f /tmp/tabby-daemon-*
```

## Build and Install

Always build to the `bin/` directory -- scripts expect binaries there. Do NOT build to the repo root (`./tabby-daemon`).

```bash
# Build individual binaries
go build -o bin/tabby-daemon ./cmd/tabby-daemon
go build -o bin/sidebar-renderer ./cmd/sidebar-renderer

# Build and install all runtime binaries
./scripts/install.sh

# Reload tmux config/plugin
tmux source-file ~/.tmux.conf
tmux run-shell ~/.tmux/plugins/tabby/tabby.tmux
```

### Restarting the Sidebar

After rebuilding, restart with:
```bash
pkill -f "tabby-daemon"; pkill -f "sidebar-renderer"; rm -f /tmp/tabby-daemon-*
TABBY_USE_RENDERER=1 /Users/b/git/tabby/scripts/toggle_sidebar_daemon.sh
```

## Key Files

| File | Purpose |
|------|---------|
| `cmd/tabby-daemon/coordinator.go` | Core rendering + interaction behavior |
| `cmd/tabby-daemon/main.go` | Daemon loops, tickers, server wiring |
| `cmd/sidebar-renderer/main.go` | Sidebar input client and modal rendering |
| `cmd/pane-header/main.go` | Pane header input client and hit testing |
| `pkg/tmux/windows.go` | tmux window/pane inventory and metadata |
| `pkg/paths/paths.go` | XDG config/state path resolution |
| `scripts/_config_path.sh` | Config path resolver for shell scripts |
| `scripts/toggle_sidebar.sh` | Main user toggle entrypoint |
| `scripts/dev-status.sh` | Fresh/stale runtime verification |
| `scripts/dev-reload.sh` | Rebuild + restart workflow (opt-in) |
| `tabby.tmux` | Hooks, keybindings, mode setup |

## Testing and Verification

```bash
# Unit/command tests
go test ./cmd/tabby-daemon ./cmd/sidebar-renderer ./cmd/pane-header

# Integration
bash tests/integration/tmux_test.sh
bash tests/integration/right_click_bindings_test.sh

# E2E harness
bash tests/e2e/run_e2e.sh stale_renderer_recovery
bash tests/e2e/run_e2e.sh window_close_removes

# Runtime freshness check after rebuild/reload
./scripts/dev-status.sh
```

## Common Issues

1. Runtime is stale after rebuild: run `./scripts/dev-status.sh`, then restart per the sidebar restart command above.
2. Click handling seems wrong: verify `tabby.tmux` root mouse bindings and pane-header pass-through behavior.
3. Sidebar/tabbar duplication: ensure mode state is correct in `@tabby_sidebar` and run `scripts/ensure_sidebar.sh`.
4. Wrong process targeted: use `pgrep -a tabby-daemon` and `pgrep -a sidebar-renderer` to confirm what is running and which session/window it owns.

## Dependencies

- Go 1.24+
- tmux 3.2+
- Bubble Tea
- Lipgloss
