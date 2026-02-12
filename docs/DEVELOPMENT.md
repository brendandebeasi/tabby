# Development

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
```
