#!/usr/bin/env bash
set -e

# shellcheck disable=SC1007  # CDPATH= is a deliberate empty assignment
PLUGIN_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# go build resolves the module from the working directory, not from the package
# path it is handed, so an absolute cmd/ path is not enough: TPM runs this hook
# with tmux's cwd, which is usually outside the repo, and the build dies with
# "go.mod file not found in current directory or any parent directory".
# shellcheck disable=SC1007  # CDPATH= is a deliberate empty assignment
CDPATH= cd -- "$PLUGIN_DIR"

if ! command -v go >/dev/null 2>&1; then
	echo "Go is not installed. Please install Go 1.24+ from https://go.dev/doc/install"
	exit 1
fi

mkdir -p "$PLUGIN_DIR/bin"

for name in "$PLUGIN_DIR"/cmd/*/; do
	name=$(basename "$name")
	go build -o "$PLUGIN_DIR/bin/$name" "./cmd/$name" || { echo "Failed to build $name"; exit 1; }
done

chmod +x "$PLUGIN_DIR/bin"/*

printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
