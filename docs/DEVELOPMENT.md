# Development

## Build

```bash
./scripts/install.sh
```

## Tests

```bash
# Reload plugin in tmux

tmux run-shell ~/.tmux/plugins/tabby/tabby.tmux

# Unit tests

go test ./pkg/...

# Integration tests (Docker)

docker build -t tabby-test -f tests/Dockerfile .
docker run tabby-test /plugin/tests/integration/tmux_test.sh

# Visual capture tests

./tests/visual/capture_test.sh
```
