# Development

## Table of Contents

- [Quick Start](#quick-start)
- [Build](#build)
- [Runtime Freshness](#runtime-freshness)
- [Tests](#tests)

Use this document for day-to-day development commands. For canonical runtime behavior,
see `docs/CURRENT_STATE.md`.

## Quick Start

```bash
./scripts/install.sh
./scripts/dev-status.sh
```

## Build

```bash
./scripts/install.sh
```

## Runtime Freshness

```bash
./scripts/dev-status.sh
./scripts/dev-reload.sh
```

## Tests

```bash
# Reload plugin in tmux

tmux run-shell ~/.tmux/plugins/tabby/tabby.tmux

# Unit tests

go test ./pkg/...
go test ./cmd/tabby-daemon

# Integration tests (Docker)

docker build -t tabby-test -f tests/Dockerfile .
docker run tabby-test /plugin/tests/integration/tmux_test.sh

# Visual capture tests

./tests/visual/capture_test.sh

# Marker picker focused integration checks

./tests/integration/marker_modal_behavior_test.sh
./tests/integration/marker_search_test.sh

# Right-click and input routing checks
./tests/integration/right_click_bindings_test.sh

# E2E harness
./tests/e2e/run_e2e.sh stale_renderer_recovery
./tests/e2e/run_e2e.sh window_close_removes
```
