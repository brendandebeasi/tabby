#!/usr/bin/env bash
set -euo pipefail

# Installs tabby's binaries into bin/. tabby.tmux runs this on startup when
# bin/ is missing or empty, so it is the first thing a new user executes.
#
# It prefers a prebuilt release tarball over compiling, because requiring a
# working Go 1.24+ toolchain at install time is the single most fragile part
# of the install path -- it is what broke in issue #66, and it rules out
# machines where installing Go is not the user's call (locked-down work
# laptops, minimal containers, small servers). Compiling remains the fallback
# and is still fully supported.

# shellcheck disable=SC1007  # CDPATH= is a deliberate empty assignment
PLUGIN_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# go build resolves the module from the working directory, not from the package
# path it is handed, so an absolute cmd/ path is not enough: TPM runs this hook
# with tmux's cwd, which is usually outside the repo, and the build dies with
# "go.mod file not found in current directory or any parent directory".
# shellcheck disable=SC1007  # CDPATH= is a deliberate empty assignment
CDPATH= cd -- "$PLUGIN_DIR"

RELEASE_BASE_URL="${TABBY_RELEASE_BASE_URL:-https://github.com/brendandebeasi/tabby/releases/latest/download}"

log() { printf 'tabby: %s\n' "$1"; }

have() { command -v "$1" >/dev/null 2>&1; }

# Maps uname output onto the GOOS/GOARCH pair the release assets are named
# after. Prints nothing and fails when this platform has no prebuilt asset,
# which is the signal to fall through to a source build.
detect_platform() {
	local os arch
	case "$(uname -s)" in
		Darwin) os=darwin ;;
		Linux) os=linux ;;
		*) return 1 ;;
	esac
	case "$(uname -m)" in
		x86_64 | amd64) arch=amd64 ;;
		arm64 | aarch64) arch=arm64 ;;
		armv7l | armv7* | armv6l | armv6*) arch=arm ;;
		*) return 1 ;;
	esac
	printf '%s_%s\n' "$os" "$arch"
}

fetch() {
	local url=$1 dest=$2
	if have curl; then
		curl -fsSL --retry 2 -o "$dest" "$url"
	elif have wget; then
		wget -q -O "$dest" "$url"
	else
		return 1
	fi
}

verify_checksum() {
	local dir=$1 file=$2 line
	# No SHA256SUMS on the release is a reason to refuse the binaries, not a
	# reason to install them unverified.
	line=$(grep -E "[[:space:]]\*?${file}\$" "$dir/SHA256SUMS" 2>/dev/null) || return 1
	(
		# shellcheck disable=SC1007  # CDPATH= is a deliberate empty assignment
		CDPATH= cd -- "$dir" || exit 1
		if have sha256sum; then
			printf '%s\n' "$line" | sha256sum -c --status -
		elif have shasum; then
			printf '%s\n' "$line" | shasum -a 256 -c --status -
		else
			exit 1
		fi
	)
}

# Returns 0 when this checkout has local modifications to tracked files. Such a
# tree would not match any published release, so a developer who wiped bin/
# should get their own code compiled rather than someone else's binaries.
is_dirty_checkout() {
	have git || return 1
	git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 1
	[ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]
}

install_prebuilt() {
	local platform=$1 asset tmp
	asset="tabby_${platform}.tar.gz"
	tmp=$(mktemp -d)
	# shellcheck disable=SC2064  # expand tmp now, while it is still in scope
	trap "rm -rf '$tmp'" RETURN

	log "downloading prebuilt binaries for ${platform}"
	fetch "$RELEASE_BASE_URL/$asset" "$tmp/$asset" || return 1
	fetch "$RELEASE_BASE_URL/SHA256SUMS" "$tmp/SHA256SUMS" || return 1
	verify_checksum "$tmp" "$asset" || {
		log "checksum mismatch on $asset"
		return 1
	}

	tar -xzf "$tmp/$asset" -C "$tmp" || return 1
	[ -d "$tmp/bin" ] || return 1

	mkdir -p "$PLUGIN_DIR/bin"
	# rm-then-cp rather than overwrite in place: replacing a running binary's
	# contents poisons macOS's cached code signature for that path and every
	# later exec is SIGKILLed. Same reason the Makefile's sync target does it.
	local f base
	for f in "$tmp"/bin/*; do
		base=$(basename "$f")
		rm -f "$PLUGIN_DIR/bin/$base"
		cp "$f" "$PLUGIN_DIR/bin/$base"
	done
	chmod +x "$PLUGIN_DIR/bin"/*

	# Go ad-hoc signs darwin/arm64 itself, but re-signing locally is cheap and
	# covers a tarball that arrived without a usable signature.
	if [ "$(uname -s)" = "Darwin" ] && have codesign; then
		for f in "$PLUGIN_DIR"/bin/*; do
			codesign --force --sign - "$f" >/dev/null 2>&1 || true
		done
	fi
	return 0
}

install_from_source() {
	if ! have go; then
		return 1
	fi
	# An unmatched glob would otherwise expand to the literal "cmd/*/" and the
	# failure would surface as the baffling `Failed to build *`.
	if [ ! -d "$PLUGIN_DIR/cmd" ] || [ -z "$(find "$PLUGIN_DIR/cmd" -mindepth 1 -maxdepth 1 -type d -print -quit)" ]; then
		log "no cmd/ directory to build from"
		return 1
	fi
	log "building from source"
	mkdir -p "$PLUGIN_DIR/bin"
	local name
	for name in "$PLUGIN_DIR"/cmd/*/; do
		name=$(basename "$name")
		go build -o "$PLUGIN_DIR/bin/$name" "./cmd/$name" || {
			echo "Failed to build $name"
			return 1
		}
	done
	chmod +x "$PLUGIN_DIR/bin"/*
	return 0
}

main() {
	local platform=""

	if [ "${TABBY_INSTALL_FROM_SOURCE:-0}" = "1" ]; then
		log "TABBY_INSTALL_FROM_SOURCE=1, skipping the prebuilt download"
	elif is_dirty_checkout && have go; then
		log "working tree has local changes, building those instead of downloading"
	elif ! platform=$(detect_platform); then
		log "no prebuilt binaries for $(uname -s)/$(uname -m)"
	else
		if install_prebuilt "$platform"; then
			log "installed prebuilt binaries for $platform"
			printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
			return 0
		fi
		log "download failed, falling back to building from source"
	fi

	if install_from_source; then
		printf "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf\n"
		return 0
	fi

	echo "tabby: could not install." >&2
	if have go; then
		echo "  Downloading prebuilt binaries did not work, and building from source" >&2
		echo "  failed too. The build output above says why." >&2
	else
		echo "  No prebuilt binaries could be downloaded for this platform, and Go is" >&2
		echo "  not installed to build from source." >&2
		echo "  Install Go 1.24+ from https://go.dev/doc/install and re-run, or" >&2
	fi
	echo "  fetch a release tarball by hand from" >&2
	echo "  https://github.com/brendandebeasi/tabby/releases and unpack its bin/" >&2
	echo "  directory into $PLUGIN_DIR/" >&2
	return 1
}

main "$@"
