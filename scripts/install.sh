#!/usr/bin/env bash
set -e

PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"

if ! command -v go >/dev/null 2>&1; then
	echo "Go is not installed. Please install Go 1.24+ from https://go.dev/doc/install"
	exit 1
fi

mkdir -p "$PLUGIN_DIR/bin"

cd "$PLUGIN_DIR"

go build -o bin/render-status ./cmd/render-status
go build -o bin/render-tab ./cmd/render-tab
go build -o bin/tabby-daemon ./cmd/tabby-daemon
go build -o bin/sidebar-renderer ./cmd/sidebar-renderer
go build -o bin/window-header ./cmd/window-header
go build -o bin/cycle-pane ./cmd/cycle-pane
go build -o bin/new-window ./cmd/new-window
go build -o bin/tabby-toggle ./cmd/tabby-toggle
go build -o bin/tabby-hook ./cmd/tabby-hook
go build -o bin/tabby-watchdog ./cmd/tabby-watchdog
go build -o bin/tabby-dev ./cmd/tabby-dev
go build -o bin/tabby-sidebar-popup ./cmd/tabby-sidebar-popup
go build -o bin/tabby-pane-picker ./cmd/tabby-pane-picker

chmod +x bin/render-status
chmod +x bin/render-tab
chmod +x bin/tabby-daemon
chmod +x bin/sidebar-renderer
chmod +x bin/window-header
chmod +x bin/cycle-pane
chmod +x bin/new-window
chmod +x bin/tabby-toggle
chmod +x bin/tabby-hook
chmod +x bin/tabby-watchdog
chmod +x bin/tabby-dev
chmod +x bin/tabby-sidebar-popup
chmod +x bin/tabby-pane-picker

printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
