#!/usr/bin/env python3
"""
capture-png-local.py — capture the active window of a local tmux session
as a PNG image. Companion to scripts/render-capture.py (which goes over
SSH to a bastion); this one runs tmux + aha on the same host.

Usage:
    python3 scripts/capture-png-local.py [--session '$1'] [--output /tmp/out.png]

Both tools share the ANSI→image renderer in render-capture.py — this file
just swaps the `ssh_run` shell runner for a local one.

Requires: tmux, aha (brew install aha), python3 + Pillow.
"""
import argparse
import importlib.util
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
RENDER_CAPTURE = os.path.join(HERE, "render-capture.py")


def load_render_capture():
    spec = importlib.util.spec_from_file_location("render_capture", RENDER_CAPTURE)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot import render-capture.py from {RENDER_CAPTURE}")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def local_run(cmd: str) -> str:
    """Run cmd in a local shell; stdout-only, stderr discarded on non-zero.

    Signature matches render-capture.ssh_run so we can monkey-patch it in.
    """
    result = subprocess.run(["bash", "-c", cmd], capture_output=True, text=True)
    return result.stdout


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--session", "-s", default="", help="tmux session id (e.g. '$1')")
    ap.add_argument("--output", "-o", default="/tmp/tabby-capture.png")
    args = ap.parse_args()

    rc = load_render_capture()
    # Redirect the bastion SSH runner to a local one. render-capture.py treats
    # ssh_run as the single shell-exec entry point, so this is the only hook
    # needed to run the same renderer locally.
    rc.ssh_run = local_run  # type: ignore[attr-defined]

    session = args.session
    if not session:
        out = local_run(
            "tmux list-sessions -F '#{session_last_attached} #{session_id}' 2>/dev/null"
        )
        lines = sorted(out.strip().splitlines(), reverse=True)
        if not lines:
            print("No tmux sessions found", file=sys.stderr)
            sys.exit(1)
        session = lines[0].split()[-1]
        print(f"Session: {session}")

    rc.render_session(session, args.output)


if __name__ == "__main__":
    main()
