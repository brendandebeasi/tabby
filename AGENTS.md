# AGENTS.md - Claude Code Guide

## Table of Contents

- [Project Overview](#project-overview)
- [Architecture](#architecture)
- [State and Modes](#state-and-modes)
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

## State and Modes

- Source-of-truth runtime state: tmux option `@tabby_sidebar`
- Values:
  - `enabled`: vertical sidebar mode
  - `horizontal`: top tabbar mode
  - `disabled`: native tmux status mode
- Per-session runtime files: `/tmp/tabby-daemon-<session>.{pid,sock,events.log,input.log}`

## Build and Install

```bash
# Build and install all runtime binaries
./scripts/install.sh

# Reload tmux config/plugin
tmux source-file ~/.tmux.conf
tmux run-shell ~/.tmux/plugins/tabby/tabby.tmux
```

## Key Files

| File | Purpose |
|------|---------|
| `cmd/tabby-daemon/coordinator.go` | Core rendering + interaction behavior |
| `cmd/tabby-daemon/main.go` | Daemon loops, tickers, server wiring |
| `cmd/sidebar-renderer/main.go` | Sidebar input client and modal rendering |
| `cmd/pane-header/main.go` | Pane header input client and hit testing |
| `pkg/tmux/windows.go` | tmux window/pane inventory and metadata |
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

1. Runtime is stale after rebuild: run `./scripts/dev-status.sh`, then restart per recommended toggle sequence.
2. Click handling seems wrong: verify `tabby.tmux` root mouse bindings and pane-header pass-through behavior.
3. Sidebar/tabbar duplication: ensure mode state is correct in `@tabby_sidebar` and run `scripts/ensure_sidebar.sh`.

## Dependencies

- Go 1.24+
- tmux 3.2+
- Bubble Tea
- Lipgloss
