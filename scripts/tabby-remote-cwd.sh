# tabby-remote-cwd.sh — report a remote shell's host + project root back to a
# local tabby sidebar, so tabby can give ssh/mosh tabs a persistent per-project
# name (keyed on "ssh://host/project-root") just like local tabs.
#
# Source this from your shell rc ON THE REMOTE HOST (the client-b* boxes), e.g.:
#
#     # ~/.bashrc or ~/.zshrc
#     [ -f ~/.tabby-remote-cwd.sh ] && . ~/.tabby-remote-cwd.sh
#
# How it works: on each prompt it prints an OSC 7700 ";tabby-cwd;HOST<US>ROOT"
# escape. Those bytes ride the ssh/mosh connection into the LOCAL tmux pane,
# where tabby's pipe-pane handler (`tabby hook osc-handler`) records them on the
# pane as @tabby_remote_cwd. No tabby binary is needed on the remote host — this
# is pure shell. HOST is `hostname -s`; ROOT is the git toplevel (or $PWD when
# not in a repo). US is the ASCII unit separator (\037), BEL (\007) terminates.

__tabby_remote_cwd() {
	# Resolve host + project root (git toplevel, else cwd).
	local __tb_host __tb_top
	__tb_host=$(hostname -s 2>/dev/null || hostname 2>/dev/null)
	__tb_top=$(git rev-parse --show-toplevel 2>/dev/null) || __tb_top=$PWD
	[ -n "$__tb_host" ] || return 0
	[ -n "$__tb_top" ] || return 0

	# Only emit when the project root changed since the last prompt — the local
	# handler debounces too, but this avoids needless bytes on every prompt.
	if [ "$__tb_top" = "$__TABBY_LAST_TOPMOST" ]; then
		return 0
	fi
	__TABBY_LAST_TOPMOST=$__tb_top

	if [ -n "$TMUX" ]; then
		# Remote shell is itself inside tmux: wrap in a DCS passthrough envelope
		# (double the ESC) so the sequence survives the remote tmux and reaches
		# the outer (local) pane. Mirrors emitOSCFallback in set_indicator.go.
		printf '\033Ptmux;\033\033]7700;tabby-cwd;%s\037%s\007\033\\' "$__tb_host" "$__tb_top"
	else
		printf '\033]7700;tabby-cwd;%s\037%s\007' "$__tb_host" "$__tb_top"
	fi
}

# Wire the reporter into the shell's pre-prompt hook.
if [ -n "$ZSH_VERSION" ]; then
	# add-zsh-hook is idempotent — sourcing twice won't double-register.
	autoload -Uz add-zsh-hook 2>/dev/null
	add-zsh-hook precmd __tabby_remote_cwd 2>/dev/null
elif [ -n "$BASH_VERSION" ]; then
	case "$PROMPT_COMMAND" in
		*__tabby_remote_cwd*) ;; # already wired
		*) PROMPT_COMMAND="__tabby_remote_cwd${PROMPT_COMMAND:+; $PROMPT_COMMAND}" ;;
	esac
fi
