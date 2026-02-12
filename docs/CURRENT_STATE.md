# Current State

Canonical reference for how Tabby works today. Use this file as the source of truth when docs disagree.

## Runtime Architecture

- `tabby-daemon` is the long-running per-session coordinator.
- `sidebar-renderer` runs per window (sidebar UI client).
- `pane-header` runs per content pane (header UI client).
- State source of truth is tmux option `@tabby_sidebar`.
- Auto-start default is enabled; disable with `@tabby_auto_start 0`.

Core runtime files (per session id `$N`):

- `/tmp/tabby-daemon-$N.pid`
- `/tmp/tabby-daemon-$N.sock`
- `/tmp/tabby-daemon-$N-events.log`
- `/tmp/tabby-daemon-$N-input.log`

## Build + Freshness Workflow

```bash
# Build/install binaries
./scripts/install.sh

# Reload tmux config
tmux source ~/.tmux.conf

# Verify current session daemon is latest build
./scripts/dev-status.sh
```

If status is `STALE`, restart daemon for the target session:

```bash
TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='edge-test' ./scripts/toggle_sidebar.sh
TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='edge-test' ./scripts/toggle_sidebar.sh
```

Optional startup controls:

```tmux
# Disable automatic sidebar startup on tmux server/session startup
set -g @tabby_auto_start 0

# Legacy gate (default is enabled unless explicitly 0)
set -g @tabby_test 0
```

## Dev Reload

```bash
# Enable once per tmux server
tmux set-option -g @tabby_dev_reload_enabled 1

# Rebuild + restart runtime (when sidebar is enabled)
./scripts/dev-reload.sh
```

`dev-reload.sh` fails non-zero and shows a loud tmux message when runtime stays stale.

## Canonical Test Commands

```bash
# Daemon/coordinator unit tests
go test ./cmd/tabby-daemon

# Integration
./tests/integration/tmux_test.sh

# Focused E2E
./tests/e2e/run_e2e.sh sidebar_toggle_open
./tests/e2e/run_e2e.sh window_close_removes
./tests/e2e/run_e2e.sh stale_renderer_recovery
./tests/e2e/run_e2e.sh split_spawns_pane_header
```

## Current Interaction Rules

- Group collapse/expand toggles only from the disclosure icon hit area.
- Full group header row is context-only (right-click menu), not left-click toggle.
- Pane header buttons are handled via direct mouse pass-through to `pane-header` (`send-keys -M`), not focus replay.
- Window/group marker search opens an in-app picker modal (Bubble Tea overlay) with fuzzy matching.
- Window close flow restores focus using tracked window history (`track_window_history.sh` + `select_previous_window.sh`).
- Pane layout/ratio continuity is maintained via `save_pane_layout.sh` and `preserve_pane_ratios.sh` hooks.

## Notes

- Use this file as the canonical reference for current runtime and workflow behavior.
- Lipgloss v2 remains pre-release; stay on Lipgloss v1 for runtime stability unless migration is explicitly requested.
