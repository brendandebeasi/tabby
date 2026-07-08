#!/usr/bin/env bash
# Run a VM test script on the tabby-dev OrbStack VM, then ALWAYS restore the host
# macOS binary. OrbStack shares /Users, so a VM `go build -o bin/tabby` clobbers
# the host Mach-O bin/tabby with a Linux ELF -> bdm1 tmux hooks then return 126.
# Wrapping every VM test in this restore guarantees the host bin is Mach-O again.
#
# Usage: scripts/vm-test.sh <vm-script-path-relative-to-repo> [args...]
set -u
SELF="$(cd "$(dirname "$(realpath "${BASH_SOURCE[0]}")")" && pwd)"
REPO="$(cd "$SELF/.." && pwd)"
# Accept either an absolute path or one relative to the repo root.
SCRIPT="$1"; shift || true
case "$SCRIPT" in
	/*) SCRIPT_ABS="$SCRIPT" ;;
	*)  SCRIPT_ABS="$REPO/$SCRIPT" ;;
esac

orb -m tabby-dev bash -lc "bash '$SCRIPT_ABS' $*"
rc=$?

echo "--- restoring host macOS bin/tabby (VM build clobbers it) ---"
if ( cd "$REPO" && go build -o bin/tabby ./cmd/tabby ); then
	arch=$(file "$REPO/bin/tabby" | grep -o 'Mach-O\|ELF')
	echo "host bin/tabby: $arch"
	[ "$arch" = "Mach-O" ] || { echo "WARNING: host bin is not Mach-O!"; }
else
	echo "WARNING: host rebuild FAILED — bin/tabby may still be a Linux ELF"
fi
exit $rc
