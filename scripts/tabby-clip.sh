# tabby-clip.sh — push text from a remote shell up to the clipboard of the
# device you are actually sitting at (laptop, phone), with no tabby binary and
# no open socket on the remote host.
#
# Source this from your shell rc ON THE REMOTE HOST (the client-b* boxes), e.g.:
#
#     # ~/.bashrc or ~/.zshrc
#     [ -f ~/.tabby-clip.sh ] && . ~/.tabby-clip.sh
#
# Then:
#
#     tabby-clip < some-file
#     some-command | tabby-clip
#     tabby-clip "a literal string"
#
# How it works: it prints an OSC 52 clipboard-set escape to the terminal. Those
# bytes ride the ssh connection into the local tmux pane, where tmux (with
# `set-clipboard on`) forwards them up the mosh/ssh link to the terminal app,
# which writes the payload to the system clipboard. Every hop is a pipe that
# already exists — nothing listens, nothing is forwarded, nothing needs a port.
#
# This is the shell twin of `tabby clip send`, which does the same thing on a
# host that has the binary. Keep the two behaving identically: a skill or agent
# picks whichever is available and should not have to care which it got.

# Largest raw payload to send in one escape.
#
# tmux, mosh and the terminal app each cap the escape's length, none of them
# report a failure, and an over-long one is dropped or cut mid-base64 so the
# clipboard ends up holding garbage. 64 KiB raw is ~87 KB encoded, under every
# limit we know of. Override by setting TABBY_CLIP_MAX before sourcing.
: "${TABBY_CLIP_MAX:=65536}"

tabby-clip() {
	local __tb_payload __tb_b64

	if [ "$#" -gt 0 ]; then
		__tb_payload=$*
	else
		__tb_payload=$(cat)
	fi

	# An empty payload would clear the clipboard rather than fill it, throwing
	# away whatever the user had copied. Do nothing instead.
	[ -n "$__tb_payload" ] || return 0

	# Keep the tail when over the cap: the oversized case is nearly always a
	# long capture or log, where the end is the part worth having.
	if [ "${#__tb_payload}" -gt "$TABBY_CLIP_MAX" ]; then
		__tb_payload=${__tb_payload: -$TABBY_CLIP_MAX}
	fi

	# -w0 is a GNU coreutils spelling and BSD base64 rejects it, so wrap
	# unconditionally and strip newlines afterwards. The escape cannot contain
	# them: a newline terminates the sequence early and the rest of the base64
	# lands on screen as text.
	__tb_b64=$(printf '%s' "$__tb_payload" | base64 | tr -d '\n')

	if [ -n "$TMUX" ]; then
		# The remote shell is itself inside tmux. That tmux would interpret a
		# bare OSC 52 and try to act on it locally, where there is no clipboard
		# worth writing to. Wrap it in a DCS passthrough envelope with the
		# inner ESC doubled so the remote tmux forwards the bytes upstream
		# verbatim. Mirrors __tabby_remote_cwd in tabby-remote-cwd.sh.
		#
		# The remote tmux needs `set -g allow-passthrough on` for this. Without
		# it the envelope is swallowed whole and nothing reaches the clipboard,
		# with no error anywhere — the send just appears to do nothing.
		printf '\033Ptmux;\033\033]52;c;%s\007\033\\' "$__tb_b64"
	else
		printf '\033]52;c;%s\007' "$__tb_b64"
	fi
}

# Convenience wrapper: send the tail of a command's output, so the common
# "that failed, get it onto my phone" move is one pipe rather than two.
#
#     make test 2>&1 | tabby-clip-tail 200
tabby-clip-tail() {
	local __tb_n=${1:-100}
	tail -n "$__tb_n" | tabby-clip
}
