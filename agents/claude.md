# Claude Dev Notes

## Table of Contents

- [Dev Reload Workflow (Opt-in)](#dev-reload-workflow-opt-in)

## Dev Reload Workflow (Opt-in)

- Enable: `tmux set-option -g @tabby_dev_reload_enabled 1`
- Source config: `tmux source ~/.tmux.conf`
- Trigger reload: `prefix + R`
- Script: `scripts/dev-reload.sh` (rebuilds binaries, restarts sidebar only if enabled)

### Behavior

- No-op if dev reload is disabled.
- Rebuilds via `scripts/install.sh`.
- Preserves sidebar state (does not enable if disabled).
- On client attach, auto-reloads when enabled.
