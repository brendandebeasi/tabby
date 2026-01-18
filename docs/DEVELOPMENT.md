# Development

## Build

```bash
./scripts/install.sh
```

## Tests

```bash
# Enable plugin in tmux

tmux set-option -g @tmux_tabs_test 1

# Unit tests

go test ./pkg/...

# Integration tests (Docker)

docker build -t tmux-tabs-test -f tests/Dockerfile .
docker run tmux-tabs-test /plugin/tests/integration/tmux_test.sh

# Visual capture tests

./tests/visual/capture_test.sh
```
