#!/usr/bin/env bash
set -e

PLUGIN_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v go >/dev/null 2>&1; then
	echo "Go is not installed. Please install Go 1.24+ from https://go.dev/doc/install"
	exit 1
fi

mkdir -p "$PLUGIN_DIR/bin"

for name in "$PLUGIN_DIR"/cmd/*/; do
	name=$(basename "$name")
	go build -o "$PLUGIN_DIR/bin/$name" "$PLUGIN_DIR/cmd/$name" || { echo "Failed to build $name"; exit 1; }
done

chmod +x "$PLUGIN_DIR/bin"/*

printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
