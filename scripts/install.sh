#!/usr/bin/env bash
set -e

PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

if ! command -v go >/dev/null 2>&1; then
	echo "Go is not installed. Please install Go 1.24+ from https://go.dev/doc/install"
	exit 1
fi

mkdir -p "$PLUGIN_DIR/bin"

cd "$PLUGIN_DIR"

go build -o bin/render-status cmd/render-status/main.go
go build -o bin/render-tab cmd/render-tab/main.go
go build -o bin/sidebar cmd/sidebar/main.go
go build -o bin/pane-bar cmd/pane-bar/main.go

chmod +x bin/render-status
chmod +x bin/render-tab
chmod +x bin/sidebar
chmod +x bin/pane-bar
chmod +x scripts/toggle_sidebar.sh
chmod +x scripts/ensure_pane_bar.sh
chmod +x scripts/signal_pane_bar.sh

printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
