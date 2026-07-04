#!/usr/bin/env python3
"""Capture and decode the byte sequence each keypress emits at the terminal.

Run this INSIDE the terminal/tmux you actually type into (over ssh/mosh is
fine). It puts the tty into raw mode and, for every keypress, prints the exact
bytes that arrived AND a decoded interpretation including modifiers (Ctrl /
Alt / Shift / Meta). Those bytes are what you bind — on the terminal side
(e.g. a `keybind = ...=text:\\x1b...` rule) or on the tmux/tabby side (via
`bind-key` / `user-keys`) — to switch windows/panes or anything else.

Why raw bytes and not evdev: when the keyboard is on one machine and tmux is
remote, the only thing crossing the ssh/mosh link is the terminal byte stream,
so that is exactly the layer a binding has to match.

IMPORTANT about modifiers: a terminal only *sends* a modifier if it encodes
one. Plain terminals drop most Cmd/Ctrl/Alt combos or fold them into a bare
character (e.g. Cmd+[ may arrive as just "["). To see rich modified sequences
(CSI-u / modifyOtherKeys) the terminal must be configured to emit them. This
tool decodes whatever actually arrives and, when a modifier was dropped, that
absence is itself the answer: you'll see the bare byte.

Quit: press Ctrl-] (shown as <0x1d>), or Ctrl-C three times.
Self-test (no tty needed): `capture-keys.py --selftest`

Output per keypress:
  hex     : space-separated bytes, e.g. 1b 5b 31 3b 35 43
  escaped : C/Python-style, paste-ready, e.g. \\x1b[1;5C
  decoded : human reading with modifiers, e.g. Ctrl+Right
"""

import os
import select
import sys
import termios
import tty

# --- decode tables -----------------------------------------------------------

# Final byte of a CSI/SS3 sequence -> key name.
CSI_FINAL_NAMES = {
    "A": "Up", "B": "Down", "C": "Right", "D": "Left",
    "E": "Begin", "F": "End", "H": "Home",
    "P": "F1", "Q": "F2", "R": "F3", "S": "F4",
    "Z": "ShiftTab",
}

# `CSI <n> ; <mod> ~` numeric key codes.
TILDE_NAMES = {
    1: "Home", 2: "Insert", 3: "Delete", 4: "End", 5: "PageUp", 6: "PageDown",
    7: "Home", 8: "End", 11: "F1", 12: "F2", 13: "F3", 14: "F4", 15: "F5",
    17: "F6", 18: "F7", 19: "F8", 20: "F9", 21: "F10", 23: "F11", 24: "F12",
}

# C0 control bytes with conventional names.
C0_SPECIAL = {
    0x00: "Ctrl+Space", 0x08: "Backspace", 0x09: "Tab", 0x0a: "Enter",
    0x0d: "Enter", 0x1b: "Escape", 0x1c: "Ctrl+\\", 0x1d: "Ctrl+]",
    0x1e: "Ctrl+^", 0x1f: "Ctrl+_", 0x7f: "Backspace",
}


def mod_list(m: int) -> list:
    """xterm modifier param -> list of modifier names. The wire value is
    1 + bitmask(Shift=1, Alt=2, Ctrl=4, Meta=8)."""
    m -= 1
    out = []
    if m & 1:
        out.append("Shift")
    if m & 2:
        out.append("Alt")
    if m & 4:
        out.append("Ctrl")
    if m & 8:
        out.append("Meta")  # Super/Cmd on some configs
    return out


def with_mods(mods: list, key: str) -> str:
    return "+".join(mods + [key]) if mods else key


def codepoint_key(code) -> str:
    """Map a Unicode codepoint (from CSI-u / modifyOtherKeys) to a key name."""
    if code is None:
        return "?"
    named = {
        0x08: "Backspace", 0x09: "Tab", 0x0a: "Enter", 0x0d: "Enter",
        0x1b: "Escape", 0x20: "Space", 0x7f: "Backspace",
    }
    if code in named:
        return named[code]
    if 0x20 <= code < 0x7f:
        return chr(code)
    return f"U+{code:04X}"


def ctrl_name(b: int) -> str:
    if b in C0_SPECIAL:
        return C0_SPECIAL[b]
    if 0x01 <= b <= 0x1a:  # Ctrl+A .. Ctrl+Z
        return "Ctrl+" + chr(b + 0x60)
    return f"0x{b:02x}"


def _params(body: bytes):
    """Split a CSI parameter string (bytes before the final letter) into ints,
    with None for empty/non-numeric slots. Returns (nums, private_prefix)."""
    s = body.decode("ascii", "ignore")
    private = ""
    if s[:1] in ("?", "<", "=", ">"):
        private, s = s[0], s[1:]
    nums = []
    for p in s.split(";"):
        nums.append(int(p) if p.isdigit() else None)
    return nums, private


def decode_csi(body: bytes):
    """Decode the bytes of a CSI sequence AFTER the leading ESC[. `body`
    includes the final letter."""
    if not body:
        return "CSI(incomplete)"
    fc = chr(body[-1])
    nums, private = _params(body[:-1])

    def mod_of(idx=1, default=1):
        return nums[idx] if len(nums) > idx and nums[idx] is not None else default

    if fc == "~":
        # modifyOtherKeys form: CSI 27 ; <mod> ; <codepoint> ~
        if len(nums) >= 3 and nums[0] == 27:
            key = codepoint_key(nums[2])
            return with_mods(mod_list(nums[1] or 1), key) + " (modifyOtherKeys)"
        code = nums[0] if nums and nums[0] is not None else None
        name = TILDE_NAMES.get(code, f"~{code}") if code is not None else "~?"
        return with_mods(mod_list(mod_of(1)), name)

    if fc == "u":  # CSI-u / fixterms: CSI <codepoint> ; <mod> u
        code = nums[0] if nums and nums[0] is not None else None
        if code is None:
            return "CSI-u(?)"
        return with_mods(mod_list(mod_of(1)), codepoint_key(code)) + " (CSI-u)"

    if fc in CSI_FINAL_NAMES:
        return with_mods(mod_list(mod_of(1)), CSI_FINAL_NAMES[fc])

    return f"CSI {private}{';'.join('' if n is None else str(n) for n in nums)}{fc}"


def decode(data: bytes) -> str:
    """Decode one keypress burst into a human string with modifiers."""
    if not data:
        return ""

    # ESC + (something that is not CSI '[' or SS3 'O') => Alt/Meta prefix.
    if len(data) >= 2 and data[0] == 0x1b and data[1] not in (0x5b, 0x4f):
        return "Alt+" + decode(data[1:])

    # CSI: ESC [ ...
    if len(data) >= 2 and data[0] == 0x1b and data[1] == 0x5b:
        return decode_csi(data[2:])

    # SS3: ESC O x  (F1-F4, app-mode arrows)
    if len(data) >= 3 and data[0] == 0x1b and data[1] == 0x4f:
        f = chr(data[2])
        return CSI_FINAL_NAMES.get(f, "SS3-" + f)

    if data == b"\x1b":
        return "Escape"

    # Single control / DEL byte.
    if len(data) == 1 and (data[0] < 0x20 or data[0] == 0x7f):
        return ctrl_name(data[0])
    if len(data) == 1 and data[0] == 0x20:
        return "Space"

    # Printable ASCII / UTF-8 character(s).
    try:
        s = data.decode("utf-8")
    except UnicodeDecodeError:
        # High-bit-set bytes that aren't valid UTF-8: legacy Meta (8th bit).
        if len(data) == 1 and data[0] >= 0x80:
            return "Meta+" + decode(bytes([data[0] & 0x7f]))
        return "(undecodable bytes)"
    if len(s) == 1:
        return s if s.isprintable() else repr(s)
    return repr(s)  # multi-char burst (paste or chained keys)


# --- pretty-printers ---------------------------------------------------------

def to_hex(data: bytes) -> str:
    return " ".join(f"{b:02x}" for b in data)


def to_escaped(data: bytes) -> str:
    out = []
    for b in data:
        if b == 0x1b:
            out.append("\\x1b")
        elif b == 0x0a:
            out.append("\\n")
        elif b == 0x0d:
            out.append("\\r")
        elif b == 0x09:
            out.append("\\t")
        elif 0x20 <= b < 0x7f:
            out.append(chr(b))
        else:
            out.append(f"\\x{b:02x}")
    return "".join(out)


# --- capture loop ------------------------------------------------------------

BURST_TIMEOUT = 0.03  # seconds; escape sequences arrive as one quick burst
QUIT_BYTE = 0x1d      # Ctrl-]

# Ask the terminal to report modified keys unambiguously while we capture.
#   \x1b[>4;2m  -> xterm modifyOtherKeys level 2 (Ctrl/Alt combos as CSI-u)
#   \x1b[>Nu    -> push kitty keyboard-protocol flags (disambiguate escape
#                  codes = 1; +report-all-keys-as-escape-codes = 8 in --all)
# Restored on exit with the matching pop/reset. Terminals that don't support
# these silently ignore the unknown CSI, so it is safe to send unconditionally.
def _enable_seq(all_keys: bool) -> str:
    flags = 1 | (8 if all_keys else 0)
    return f"\x1b[>4;2m\x1b[>{flags}u"


_DISABLE_SEQ = "\x1b[<u\x1b[>4;0m"


def read_burst(fd: int) -> bytes:
    """Block for one byte, then drain everything that arrives within the burst
    window. Groups a multi-byte escape sequence into one event while still
    treating a lone ESC keypress as its own event."""
    first = os.read(fd, 1)
    if not first:
        return b""
    data = bytearray(first)
    while True:
        r, _, _ = select.select([fd], [], [], BURST_TIMEOUT)
        if not r:
            break
        more = os.read(fd, 64)
        if not more:
            break
        data.extend(more)
    return bytes(data)


def capture(all_keys: bool = False) -> int:
    if not sys.stdin.isatty():
        sys.stderr.write("capture-keys: stdin is not a tty; run it directly "
                         "in your terminal, not through a pipe.\n")
        return 1

    fd = sys.stdin.fileno()
    old = termios.tcgetattr(fd)

    print("=== tabby key capture ===")
    print("Press any key or shortcut to see its exact bytes + decoded keys.")
    print("Requesting modifyOtherKeys + kitty keyboard protocol so Ctrl/Alt/")
    print("Meta combos arrive as CSI-u. Notes on macOS/Ghostty:")
    print("  - Cmd never arrives raw; map it (keybind = cmd+x=text:\\x1b...).")
    print("  - Option needs `macos-option-as-alt = true` to become Alt.")
    print("  - Shift on letters is the capital itself, not a separate mod.")
    if all_keys:
        print("  - --all: every key (even plain letters) reported as CSI-u.")
    print("Quit: Ctrl-]  (or Ctrl-C x3)\n")
    sys.stdout.flush()

    ctrl_c = 0
    try:
        tty.setraw(fd)
        os.write(fd, _enable_seq(all_keys).encode())
        while True:
            data = read_burst(fd)
            if not data:
                break
            if len(data) == 1 and data[0] == QUIT_BYTE:
                break
            if len(data) == 1 and data[0] == 0x03:  # Ctrl-C
                ctrl_c += 1
                if ctrl_c >= 3:
                    break
            else:
                ctrl_c = 0

            # '\r\n' because the tty is raw: a bare '\n' would not return the
            # cursor to column 0.
            sys.stdout.write(
                f"hex     : {to_hex(data)}\r\n"
                f"escaped : {to_escaped(data)}\r\n"
                f"decoded : {decode(data)}\r\n"
                f"---\r\n"
            )
            sys.stdout.flush()
    finally:
        try:
            os.write(fd, _DISABLE_SEQ.encode())
        except OSError:
            pass
        termios.tcsetattr(fd, termios.TCSADRAIN, old)

    print("\nDone. Copy the 'escaped' form of the shortcut you want and bind")
    print("that sequence (tmux bind-key / user-keys, or a terminal keybind).")
    return 0


# --- self-test ---------------------------------------------------------------

def selftest() -> int:
    cases = [
        (b"a", "a"),
        (b" ", "Space"),
        (b"\x1b", "Escape"),
        (b"\x7f", "Backspace"),
        (b"\x09", "Tab"),
        (b"\x0d", "Enter"),
        (b"\x01", "Ctrl+a"),
        (b"\x1a", "Ctrl+z"),
        (b"\x1d", "Ctrl+]"),
        (b"\x1ba", "Alt+a"),
        (b"\x1b\x01", "Alt+Ctrl+a"),
        (b"\x1b[A", "Up"),
        (b"\x1b[B", "Down"),
        (b"\x1b[C", "Right"),
        (b"\x1b[D", "Left"),
        (b"\x1b[H", "Home"),
        (b"\x1b[1;5A", "Ctrl+Up"),
        (b"\x1b[1;2C", "Shift+Right"),
        (b"\x1b[1;3D", "Alt+Left"),
        (b"\x1b[1;9D", "Meta+Left"),
        (b"\x1b[1;6A", "Shift+Ctrl+Up"),
        (b"\x1b[5~", "PageUp"),
        (b"\x1b[6~", "PageDown"),
        (b"\x1b[3~", "Delete"),
        (b"\x1b[5;5~", "Ctrl+PageUp"),
        (b"\x1b[15~", "F5"),
        (b"\x1b[24~", "F12"),
        (b"\x1bOP", "F1"),
        (b"\x1bOR", "F3"),
        (b"\x1b[97;5u", "Ctrl+a (CSI-u)"),
        (b"\x1b[13;2u", "Shift+Enter (CSI-u)"),
        (b"\x1b[27;3;91~", "Alt+[ (modifyOtherKeys)"),   # observed live
        (b"\x1b[27;3;93~", "Alt+] (modifyOtherKeys)"),   # observed live
        (b"\x1b[27;5;9~", "Ctrl+Tab (modifyOtherKeys)"),
        (b"\x1b[27;2;13~", "Shift+Enter (modifyOtherKeys)"),
        (b"\x1b[Z", "ShiftTab"),
        (b"\x1b{", "Alt+{"),        # tabby's cmd+[ terminal keybind arrives thus
        (b"\x1b}", "Alt+}"),        # tabby's cmd+]
        (b"\xc3\xa9", "é"),    # UTF-8 e-acute
    ]
    ok = True
    for data, expect in cases:
        got = decode(data)
        status = "OK" if got == expect else "FAIL"
        if got != expect:
            ok = False
        print(f"[{status}] {to_escaped(data):<16} -> {got!r}"
              + ("" if got == expect else f"   (expected {expect!r})"))
    print("ALL OK" if ok else "SOME FAILED")
    return 0 if ok else 1


def main(argv) -> int:
    if "--selftest" in argv:
        return selftest()
    return capture(all_keys="--all" in argv)


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except KeyboardInterrupt:
        pass
